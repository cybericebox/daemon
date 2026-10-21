package bannerUseCase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

func row(text string, mod func(*postgres.SiteBanner)) postgres.SiteBanner {
	r := postgres.SiteBanner{ID: uuid.Must(uuid.NewV7()), Text: text, Level: "info", Audience: "everyone", IsActive: true, TargetMode: "all"}
	if mod != nil {
		mod(&r)
	}
	return r
}

func ids(items []siteBannerModel.Banner) []string {
	out := make([]string, 0, len(items))
	for _, b := range items {
		out = append(out, b.Text)
	}
	return out
}

func TestVisibleSiteBannersFiltersTheSnapshotPerViewer(t *testing.T) {
	event, other, user := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	rows := []postgres.SiteBanner{
		row("all", nil),
		row("admin only", func(r *postgres.SiteBanner) { r.TargetMode, r.TargetSites = "selected", []string{"admin"} }),
		row("this event by target", func(r *postgres.SiteBanner) { r.TargetMode, r.TargetEventIds = "selected", []uuid.UUID{event} }),
		row("event scoped", func(r *postgres.SiteBanner) { r.ScopeEventID = uuid.NullUUID{UUID: event, Valid: true} }),
		row("other event scoped", func(r *postgres.SiteBanner) { r.ScopeEventID = uuid.NullUUID{UUID: other, Valid: true} }),
		row("not started", func(r *postgres.SiteBanner) { r.ActiveFrom.Time, r.ActiveFrom.Valid = future, true }),
		row("ended", func(r *postgres.SiteBanner) { r.ActiveTo.Time, r.ActiveTo.Valid = past, true }),
		row("signed in", func(r *postgres.SiteBanner) { r.Audience = "signed_in" }),
		row("participants", func(r *postgres.SiteBanner) {
			r.Audience, r.ScopeEventID = "participants", uuid.NullUUID{UUID: event, Valid: true}
		}),
		row("disabled", func(r *postgres.SiteBanner) { r.IsActive = false }),
	}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().ListActiveSiteBanners(gomock.Any()).Return(rows, nil).Times(1)
	member := false
	u := NewSiteBannerUseCase(Dependencies{Repo: q, IsEventMember: func(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return member, nil }})
	u.now = func() time.Time { return now }
	ctx := context.Background()

	got, err := u.VisibleSiteBanners(ctx, siteModel.Site{Kind: siteModel.Main}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"all"}; len(got) != 1 || got[0].Text != want[0] {
		t.Fatalf("guest on main: %v", ids(got))
	}
	got, _ = u.VisibleSiteBanners(ctx, siteModel.Site{Kind: siteModel.Admin}, &user)
	if len(got) != 3 { // all, admin only, signed in
		t.Fatalf("user on admin: %v", ids(got))
	}
	got, _ = u.VisibleSiteBanners(ctx, siteModel.Site{Kind: siteModel.Event, EventID: &event}, nil)
	if len(got) != 3 { // all, this event by target, event scoped
		t.Fatalf("guest on the event: %v", ids(got))
	}
	got, _ = u.VisibleSiteBanners(ctx, siteModel.Site{Kind: siteModel.Event, EventID: &event}, &user)
	if len(got) != 4 { // + signed in; not a member
		t.Fatalf("user on the event: %v", ids(got))
	}
	member = true
	got, _ = u.VisibleSiteBanners(ctx, siteModel.Site{Kind: siteModel.Event, EventID: &event}, &user)
	if len(got) != 5 {
		t.Fatalf("member on the event: %v", ids(got))
	}
}

func TestSnapshotIsSharedUntilItExpiresOrABannerIsWritten(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	clock := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	u := NewSiteBannerUseCase(Dependencies{Repo: q})
	u.now = func() time.Time { return clock }
	site := siteModel.Site{Kind: siteModel.Main}
	ctx := context.Background()
	call := func() []siteBannerModel.Banner {
		t.Helper()
		got, err := u.VisibleSiteBanners(ctx, site, nil)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}

	q.EXPECT().ListActiveSiteBanners(gomock.Any()).Return([]postgres.SiteBanner{row("one", nil)}, nil)
	for range 5 {
		if got := call(); len(got) != 1 {
			t.Fatalf("got %v", ids(got))
		}
	}

	// A write on this replica drops the snapshot at once.
	q.EXPECT().CreateSiteBanner(gomock.Any(), gomock.Any()).Return(postgres.SiteBanner{}, nil)
	if _, err := u.CreateSiteBanner(ctx, nil, uuid.Must(uuid.NewV7()), validInput()); err != nil {
		t.Fatal(err)
	}
	q.EXPECT().ListActiveSiteBanners(gomock.Any()).Return([]postgres.SiteBanner{row("one", nil), row("two", nil)}, nil)
	if got := call(); len(got) != 2 {
		t.Fatalf("after write: %v", ids(got))
	}
}

func TestSnapshotReloadsAfterTTLAndSurvivesAFailedReload(t *testing.T) {
	var s bannerSnapshot
	calls := 0
	items := []siteBannerModel.Banner{{Text: "kept"}}
	load := func(context.Context) ([]siteBannerModel.Banner, error) {
		calls++
		if calls == 2 {
			return nil, errors.New("db down")
		}
		return items, nil
	}
	ctx := context.Background()
	if _, err := s.get(ctx, time.Now(), load); err != nil {
		t.Fatal(err)
	}
	if _, err := s.get(ctx, time.Now(), load); err != nil || calls != 1 {
		t.Fatalf("fresh snapshot reloaded: calls=%d err=%v", calls, err)
	}
	s.mu.Lock()
	s.loadedAt = time.Now().Add(-2 * snapshotTTL)
	s.mu.Unlock()
	got, err := s.get(ctx, time.Now(), load)
	if err != nil || len(got) != 1 || calls != 2 {
		t.Fatalf("a failed reload must serve the old snapshot: %v %v calls=%d", got, err, calls)
	}
}
