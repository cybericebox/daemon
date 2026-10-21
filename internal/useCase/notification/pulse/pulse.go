// Package pulseUseCase answers what a client of a site has to know cheaply and often: the version
// of the banners it should show and the state of the signed-in user's inbox. The heavy data (the
// banner list, the new inbox items) is fetched by the client only when the pulse says it changed.
package pulseUseCase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/rbac"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// Banner kinds.
const (
	// KindSite is an authored site banner.
	KindSite = "site"
	// KindSystem is a banner the platform raises itself (the results freeze).
	KindSystem = "system"
	// KindPersonal is a banner notification addressed to the signed-in user.
	KindPersonal = "personal"
)

// FreezeBannerIDPrefix starts the stable id of the results-freeze banner of an event.
const FreezeBannerIDPrefix = "system-results-freeze:"

type (
	// SiteBanners is the in-memory banner snapshot.
	SiteBanners interface {
		VisibleSiteBanners(ctx context.Context, site siteModel.Site, userID *uuid.UUID) ([]siteBannerModel.Banner, error)
	}
	// Freeze is the results-freeze state of an event for one viewer.
	Freeze interface {
		ResultsFreezeBanner(ctx context.Context, eventID uuid.UUID, access eventUseCase.ResultsAccess) (eventUseCase.ResultsFreezeView, error)
	}
	// Inbox is the user's inbox: the pulse numbers and the personal banners.
	Inbox interface {
		InboxPulse(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) (inboxModel.Pulse, error)
		ListBanners(ctx context.Context, userID uuid.UUID, scope *uuid.UUID) ([]inboxModel.InAppNotification, error)
	}

	Dependencies struct {
		Banners SiteBanners
		Freeze  Freeze
		Inbox   Inbox
	}

	PulseUseCase struct {
		banners SiteBanners
		freeze  Freeze
		inbox   Inbox
		now     func() time.Time
	}

	// Viewer is who asks; UserID is nil for a guest.
	Viewer struct {
		UserID *uuid.UUID
		Role   rbac.Role
	}

	// Banner is one banner of the list, whatever its source.
	Banner struct {
		ID          string
		Kind        string
		Title       string
		Text        string
		LinkURL     string
		LinkLabel   string
		Level       siteBannerModel.Level
		Dismissible bool
		// Version changes on every edit, so a dismissal of the old text does not hide the edited banner.
		Version int64

		eventScoped bool
		createdAt   time.Time
	}

	// BannerList is the full list and the version the pulse reports for it.
	BannerList struct {
		Items   []Banner
		Version string
	}

	InboxPulse struct {
		// Cursor is the newest inbox item, empty when there is none.
		Cursor string
		Unread int64
	}

	// View is the pulse. Inbox is nil for a guest.
	View struct {
		Banners string
		Inbox   *InboxPulse
	}
)

func New(deps Dependencies) *PulseUseCase {
	return &PulseUseCase{banners: deps.Banners, freeze: deps.Freeze, inbox: deps.Inbox, now: time.Now}
}

// Pulse is the cheap call: a banner version computed from the in-memory snapshot and the freeze state,
// plus, for a signed-in user, one indexed inbox query.
func (p *PulseUseCase) Pulse(ctx context.Context, site siteModel.Site, viewer Viewer) (View, error) {
	items, err := p.siteBanners(ctx, site, viewer)
	if err != nil {
		return View{}, err
	}
	if viewer.UserID == nil {
		return View{Banners: bannersVersion(items, 0, time.Time{})}, nil
	}
	pulse, err := p.inbox.InboxPulse(ctx, *viewer.UserID, site.InboxScope())
	if err != nil {
		return View{}, err
	}
	view := View{Banners: bannersVersion(items, pulse.PersonalBanners, pulse.PersonalBannersLatest), Inbox: &InboxPulse{Unread: pulse.Unread}}
	if pulse.Cursor != nil {
		view.Inbox.Cursor = pulse.Cursor.String()
	}
	return view, nil
}

// Banners is the full list the pulse version stands for: the site banners, the freeze banner when the
// freeze applies to this viewer, and the user's personal banners.
func (p *PulseUseCase) Banners(ctx context.Context, site siteModel.Site, viewer Viewer) (BannerList, error) {
	items, err := p.siteBanners(ctx, site, viewer)
	if err != nil {
		return BannerList{}, err
	}
	var (
		count  int64
		latest time.Time
	)
	if viewer.UserID != nil {
		personal, listErr := p.inbox.ListBanners(ctx, *viewer.UserID, site.InboxScope())
		if listErr != nil {
			return BannerList{}, listErr
		}
		for _, n := range personal {
			count++
			if n.CreatedAt.After(latest) {
				latest = n.CreatedAt
			}
		}
		version := bannersVersion(items, count, latest)
		for _, n := range personal {
			items = append(items, personalBanner(n))
		}
		return BannerList{Items: items, Version: version}, nil
	}
	return BannerList{Items: items, Version: bannersVersion(items, 0, time.Time{})}, nil
}

// siteBanners are the site and system banners of the viewer, in display order.
func (p *PulseUseCase) siteBanners(ctx context.Context, site siteModel.Site, viewer Viewer) ([]Banner, error) {
	banners, err := p.banners.VisibleSiteBanners(ctx, site, viewer.UserID)
	if err != nil {
		return nil, err
	}
	items := make([]Banner, 0, len(banners)+1)
	for _, b := range banners {
		items = append(items, Banner{
			ID: b.ID.String(), Kind: KindSite, Text: b.Text, LinkURL: b.LinkURL, LinkLabel: b.LinkLabel, Level: b.Level,
			Dismissible: b.Dismissible, Version: b.UpdatedAt.UnixMilli(), eventScoped: b.ScopeEventID != nil, createdAt: b.CreatedAt,
		})
	}
	if site.EventID != nil {
		freeze, applies, freezeErr := p.freezeBanner(ctx, *site.EventID, viewer)
		if freezeErr != nil {
			return nil, freezeErr
		}
		if applies {
			items = append(items, freeze)
		}
	}
	sortBanners(items)
	return items, nil
}

// freezeBanner is the synthesized results-freeze banner, only while the freeze applies to this viewer.
func (p *PulseUseCase) freezeBanner(ctx context.Context, eventID uuid.UUID, viewer Viewer) (Banner, bool, error) {
	access := eventUseCase.ResultsAccess{UserID: viewer.UserID, Role: viewer.Role}
	if viewer.UserID == nil {
		access.Role = rbac.RolePublic
	}
	view, err := p.freeze.ResultsFreezeBanner(ctx, eventID, access)
	if err != nil {
		return Banner{}, false, err
	}
	if !view.Applied || strings.TrimSpace(view.BannerText) == "" {
		return Banner{}, false, nil
	}
	level := eventConfigModel.FreezeBannerLevelOrDefault(view.BannerLevel)
	var frozenAt time.Time
	if view.FrozenAt != nil {
		frozenAt = *view.FrozenAt
	}
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%s|%s|%d", view.BannerText, level, frozenAt.UnixMilli())
	return Banner{
		ID: FreezeBannerIDPrefix + eventID.String(), Kind: KindSystem, Text: view.BannerText, Level: level,
		Version: int64(h.Sum64() & math.MaxInt64), eventScoped: true, createdAt: frozenAt,
	}, true, nil
}

func personalBanner(n inboxModel.InAppNotification) Banner {
	level := siteBannerModel.LevelInfo
	switch strings.ToLower(n.Tone) {
	case "warning":
		level = siteBannerModel.LevelWarning
	case "danger", "critical", "error":
		level = siteBannerModel.LevelCritical
	}
	return Banner{
		ID: n.ID.String(), Kind: KindPersonal, Title: n.Title, Text: n.Body, LinkURL: n.Link, Level: level,
		Dismissible: n.Dismissible, Version: n.CreatedAt.UnixMilli(), createdAt: n.CreatedAt,
	}
}

var levelRanks = map[siteBannerModel.Level]int{siteBannerModel.LevelCritical: 0, siteBannerModel.LevelWarning: 1, siteBannerModel.LevelInfo: 2} //nolint:gochecknoglobals,mnd // a constant table

func levelRank(l siteBannerModel.Level) int {
	if rank, ok := levelRanks[l]; ok {
		return rank
	}
	return len(levelRanks)
}

// sortBanners orders by severity, platform banners before event ones, then newest first.
func sortBanners(items []Banner) {
	slices.SortStableFunc(items, func(a, b Banner) int {
		if c := levelRank(a.Level) - levelRank(b.Level); c != 0 {
			return c
		}
		if a.eventScoped != b.eventScoped {
			if !a.eventScoped {
				return -1
			}
			return 1
		}
		if c := b.createdAt.Compare(a.createdAt); c != 0 {
			return c
		}
		return strings.Compare(b.ID, a.ID)
	})
}

// bannersVersion is a short stamp of the shown banners: any add, edit, expiry, start or removal
// changes it. The personal banners enter as their count and newest creation time.
func bannersVersion(items []Banner, personalCount int64, personalLatest time.Time) string {
	h := sha256.New()
	for _, b := range items {
		_, _ = fmt.Fprintf(h, "%s:%d;", b.ID, b.Version)
	}
	if personalCount > 0 {
		_, _ = fmt.Fprintf(h, "p:%d:%s", personalCount, strconv.FormatInt(personalLatest.UnixMicro(), 10))
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}
