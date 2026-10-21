package pulseUseCase

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/rbac"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type fakeBanners struct{ items []siteBannerModel.Banner }

func (f *fakeBanners) VisibleSiteBanners(context.Context, siteModel.Site, *uuid.UUID) ([]siteBannerModel.Banner, error) {
	return f.items, nil
}

type fakeFreeze struct {
	view     eventUseCase.ResultsFreezeView
	accesses []eventUseCase.ResultsAccess
}

func (f *fakeFreeze) ResultsFreezeBanner(_ context.Context, _ uuid.UUID, access eventUseCase.ResultsAccess) (eventUseCase.ResultsFreezeView, error) {
	f.accesses = append(f.accesses, access)
	return f.view, nil
}

type fakeInbox struct {
	pulse    inboxModel.Pulse
	banners  []inboxModel.InAppNotification
	scopes   []*uuid.UUID
	pulseHit int
}

func (f *fakeInbox) InboxPulse(_ context.Context, _ uuid.UUID, scope *uuid.UUID) (inboxModel.Pulse, error) {
	f.pulseHit++
	f.scopes = append(f.scopes, scope)
	return f.pulse, nil
}

func (f *fakeInbox) ListBanners(context.Context, uuid.UUID, *uuid.UUID) ([]inboxModel.InAppNotification, error) {
	return f.banners, nil
}

type rig struct {
	banners *fakeBanners
	freeze  *fakeFreeze
	inbox   *fakeInbox
	uc      *PulseUseCase
}

func newRig() *rig {
	r := &rig{banners: &fakeBanners{}, freeze: &fakeFreeze{}, inbox: &fakeInbox{}}
	r.uc = New(Dependencies{Banners: r.banners, Freeze: r.freeze, Inbox: r.inbox})
	return r
}

func banner(text string, updated time.Time) siteBannerModel.Banner {
	return siteBannerModel.Banner{ID: uuid.Must(uuid.NewV7()), Text: text, Level: siteBannerModel.LevelInfo, UpdatedAt: updated, CreatedAt: updated}
}

var (
	t0     = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	guest  = Viewer{Role: rbac.RolePublic}
	mainUI = siteModel.Site{Kind: siteModel.Main}
)

func version(t *testing.T, r *rig, site siteModel.Site, v Viewer) string {
	t.Helper()
	view, err := r.uc.Pulse(context.Background(), site, v)
	if err != nil {
		t.Fatal(err)
	}
	list, err := r.uc.Banners(context.Background(), site, v)
	if err != nil {
		t.Fatal(err)
	}
	if view.Banners != list.Version {
		t.Fatalf("the pulse version %q differs from the list version %q", view.Banners, list.Version)
	}
	return view.Banners
}

func TestPulseBannerVersionChangesWithTheVisibleSet(t *testing.T) {
	r := newRig()
	empty := version(t, r, mainUI, guest)

	one := banner("Maintenance", t0)
	r.banners.items = []siteBannerModel.Banner{one}
	added := version(t, r, mainUI, guest)
	if added == empty {
		t.Fatal("a new banner must change the version")
	}
	if same := version(t, r, mainUI, guest); same != added {
		t.Fatal("the version must be stable while nothing changes")
	}

	one.UpdatedAt = t0.Add(time.Minute)
	r.banners.items = []siteBannerModel.Banner{one}
	if edited := version(t, r, mainUI, guest); edited == added {
		t.Fatal("an edit must change the version")
	}

	// An expired banner is no longer in the visible set.
	r.banners.items = nil
	if expired := version(t, r, mainUI, guest); expired != empty {
		t.Fatal("expiry must bring the version back to the empty set")
	}
}

func TestFreezeBannerIsSynthesizedOnlyWhereItApplies(t *testing.T) {
	event := uuid.Must(uuid.NewV7())
	site := siteModel.Site{Kind: siteModel.Event, EventID: &event}
	r := newRig()
	before := version(t, r, site, guest)

	// The freeze does not apply (or the viewer may not read the results): nothing, never an error.
	list, err := r.uc.Banners(context.Background(), site, guest)
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("no freeze: %+v %v", list, err)
	}

	frozenAt := t0
	r.freeze.view = eventUseCase.ResultsFreezeView{Applied: true, Active: true, FrozenAt: &frozenAt, BannerText: "Підсумки згодом.", BannerLevel: siteBannerModel.LevelCritical}
	started := version(t, r, site, guest)
	if started == before {
		t.Fatal("the start of the freeze must change the version")
	}
	list, _ = r.uc.Banners(context.Background(), site, guest)
	if len(list.Items) != 1 {
		t.Fatalf("items = %+v", list.Items)
	}
	got := list.Items[0]
	if got.ID != FreezeBannerIDPrefix+event.String() || got.Kind != KindSystem || got.Text != "Підсумки згодом." || got.Level != siteBannerModel.LevelCritical || got.Dismissible {
		t.Fatalf("freeze banner = %+v", got)
	}

	// A new text changes the version of the banner and of the list.
	r.freeze.view.BannerText = "Інший текст."
	if edited := version(t, r, site, guest); edited == started {
		t.Fatal("a changed freeze text must change the version")
	}

	// Staff do not get it: the use case says so through Applied.
	r.freeze.view = eventUseCase.ResultsFreezeView{Active: true, Applied: false, BannerText: ""}
	if staff := version(t, r, site, Viewer{UserID: new(uuid.Must(uuid.NewV7())), Role: rbac.RoleAdmin}); staff != before {
		t.Fatal("a viewer the freeze does not apply to must see the plain version")
	}

	// A platform site never asks for the freeze.
	n := len(r.freeze.accesses)
	version(t, r, mainUI, guest)
	if len(r.freeze.accesses) != n {
		t.Fatal("the freeze was read for a platform site")
	}
}

func TestFreezeBannerIsReadAsTheViewer(t *testing.T) {
	event, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	site := siteModel.Site{Kind: siteModel.Event, EventID: &event}
	r := newRig()
	if _, err := r.uc.Banners(context.Background(), site, guest); err != nil {
		t.Fatal(err)
	}
	if _, err := r.uc.Banners(context.Background(), site, Viewer{UserID: &user, Role: rbac.RoleUser}); err != nil {
		t.Fatal(err)
	}
	if a := r.freeze.accesses[0]; a.UserID != nil || a.Role != rbac.RolePublic {
		t.Fatalf("guest access = %+v", a)
	}
	if a := r.freeze.accesses[1]; a.UserID == nil || *a.UserID != user || a.Role != rbac.RoleUser {
		t.Fatalf("user access = %+v", a)
	}
}

func TestPulseInbox(t *testing.T) {
	event, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	r := newRig()
	view, err := r.uc.Pulse(context.Background(), mainUI, guest)
	if err != nil || view.Inbox != nil || r.inbox.pulseHit != 0 {
		t.Fatalf("guest: %+v %v hits=%d", view, err, r.inbox.pulseHit)
	}

	signedIn := Viewer{UserID: &user, Role: rbac.RoleUser}
	view, err = r.uc.Pulse(context.Background(), mainUI, signedIn)
	if err != nil || view.Inbox == nil || view.Inbox.Cursor != "" || view.Inbox.Unread != 0 {
		t.Fatalf("empty inbox: %+v %v", view, err)
	}
	if got := r.inbox.scopes[0]; got == nil || *got != inboxModel.PlatformScope {
		t.Fatalf("a platform site must use the platform scope, got %v", got)
	}

	cursor := inboxModel.Cursor{ID: uuid.Must(uuid.NewV7()), CreatedAt: t0}
	r.inbox.pulse = inboxModel.Pulse{Cursor: &cursor, Unread: 4}
	view, err = r.uc.Pulse(context.Background(), siteModel.Site{Kind: siteModel.Event, EventID: &event}, signedIn)
	if err != nil || view.Inbox.Cursor != cursor.String() || view.Inbox.Unread != 4 {
		t.Fatalf("inbox pulse: %+v %v", view, err)
	}
	if got := r.inbox.scopes[1]; got == nil || *got != event {
		t.Fatalf("an event site must use its event scope, got %v", got)
	}
	if !strings.HasSuffix(view.Inbox.Cursor, "_"+cursor.ID.String()) {
		t.Fatalf("cursor form: %q", view.Inbox.Cursor)
	}
	if back, err := inboxModel.ParseCursor(view.Inbox.Cursor); err != nil || back.ID != cursor.ID || !back.CreatedAt.Equal(cursor.CreatedAt) {
		t.Fatalf("the pulse cursor must be the since value: %v %v", back, err)
	}
}

func TestPersonalBannersAreMergedAndStampedIntoTheVersion(t *testing.T) {
	user := uuid.Must(uuid.NewV7())
	r := newRig()
	signedIn := Viewer{UserID: &user, Role: rbac.RoleUser}
	plain := version(t, r, mainUI, signedIn)

	n := inboxModel.InAppNotification{ID: uuid.Must(uuid.NewV7()), Title: "Привіт", Body: "Текст", Tone: "warning", Dismissible: true, Surface: "banner", CreatedAt: t0}
	r.inbox.banners = []inboxModel.InAppNotification{n}
	r.inbox.pulse = inboxModel.Pulse{PersonalBanners: 1, PersonalBannersLatest: t0}
	with := version(t, r, mainUI, signedIn)
	if with == plain {
		t.Fatal("a personal banner must change the version")
	}
	list, _ := r.uc.Banners(context.Background(), mainUI, signedIn)
	if len(list.Items) != 1 || list.Items[0].Kind != KindPersonal || list.Items[0].Level != siteBannerModel.LevelWarning || list.Items[0].Text != "Текст" || !list.Items[0].Dismissible {
		t.Fatalf("items = %+v", list.Items)
	}
	// A guest never gets them.
	if g := version(t, r, mainUI, guest); g != plain {
		t.Fatal("a guest must not see personal banners")
	}
}

func TestBannersAreSortedBySeverityThenPlatformFirst(t *testing.T) {
	event := uuid.Must(uuid.NewV7())
	r := newRig()
	info := banner("info", t0.Add(3*time.Hour))
	warn := banner("warn", t0)
	scoped := banner("critical event", t0.Add(time.Hour))
	scoped.Level, scoped.ScopeEventID = siteBannerModel.LevelCritical, &event
	crit := banner("critical platform", t0)
	crit.Level = siteBannerModel.LevelCritical
	warn.Level = siteBannerModel.LevelWarning
	r.banners.items = []siteBannerModel.Banner{info, warn, scoped, crit}
	list, err := r.uc.Banners(context.Background(), siteModel.Site{Kind: siteModel.Event, EventID: &event}, guest)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, b := range list.Items {
		got = append(got, b.Text)
	}
	if want := "critical platform,critical event,warn,info"; strings.Join(got, ",") != want {
		t.Fatalf("order = %v", got)
	}
}
