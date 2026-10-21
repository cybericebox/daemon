package bannerUseCase

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"golang.org/x/sync/singleflight"

	siteModel "github.com/cybericebox/daemon/internal/model/site"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

// snapshotTTL is how long the in-memory banner snapshot is served without a reload. Writes on this
// replica drop it at once; the other replicas pick a change up within the TTL.
const (
	snapshotTTL     = 10 * time.Second
	snapshotTimeout = 5 * time.Second
)

// bannerSnapshot holds every enabled banner that has not ended, of every scope, so viewers' banner
// lists and the pulse version are computed in memory instead of by a query per request.
type bannerSnapshot struct {
	mu       sync.Mutex
	items    []siteBannerModel.Banner
	loadedAt time.Time
	fresh    bool
	gen      uint64
	flight   singleflight.Group
}

func (s *bannerSnapshot) invalidate() {
	s.mu.Lock()
	s.gen++
	s.fresh = false
	s.mu.Unlock()
}

// get returns the snapshot, reloading it (once for all concurrent callers) when it is older than the
// TTL or was invalidated. A failed reload serves the previous snapshot when there is one.
func (s *bannerSnapshot) get(ctx context.Context, now time.Time, load func(context.Context) ([]siteBannerModel.Banner, error)) ([]siteBannerModel.Banner, error) {
	s.mu.Lock()
	if s.fresh && now.Sub(s.loadedAt) < snapshotTTL {
		items := s.items
		s.mu.Unlock()
		return items, nil
	}
	s.mu.Unlock()
	v, err, _ := s.flight.Do("snapshot", func() (any, error) {
		s.mu.Lock()
		gen := s.gen
		s.mu.Unlock()
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), snapshotTimeout)
		defer cancel()
		items, loadErr := load(loadCtx)
		s.mu.Lock()
		defer s.mu.Unlock()
		if loadErr != nil {
			if s.items != nil {
				return s.items, nil
			}
			return nil, loadErr
		}
		s.items = items
		if items == nil {
			s.items = []siteBannerModel.Banner{}
		}
		// A write that landed while the load ran leaves the snapshot stale.
		if gen == s.gen {
			s.fresh = true
			s.loadedAt = time.Now()
		}
		return s.items, nil
	})
	if err != nil {
		return nil, err
	}
	items, _ := v.([]siteBannerModel.Banner)
	return items, nil
}

// VisibleSiteBanners returns the site banners a viewer sees now on site, newest first: the platform
// ones plus those of the site's event, filtered by their window, targeting and audience. userID is nil
// for a guest. It reads the in-memory snapshot, not the database.
func (u *SiteBannerUseCase) VisibleSiteBanners(ctx context.Context, site siteModel.Site, userID *uuid.UUID) ([]siteBannerModel.Banner, error) {
	now := u.now()
	all, err := u.snapshot.get(ctx, now, u.banners.Active)
	if err != nil {
		return nil, err
	}
	member := func() bool { return false }
	if userID != nil && site.EventID != nil && u.isMember != nil {
		var (
			once   sync.Once
			result bool
		)
		member = func() bool {
			once.Do(func() {
				ok, memberErr := u.isMember(ctx, *site.EventID, *userID)
				result = memberErr == nil && ok
			})
			return result
		}
	}
	out := make([]siteBannerModel.Banner, 0, len(all))
	for _, b := range all {
		if visibleTo(b, site, userID != nil, member, now) {
			out = append(out, b)
		}
	}
	return out, nil
}

// visibleTo mirrors the ListVisibleSiteBanners query on one banner.
func visibleTo(b siteBannerModel.Banner, site siteModel.Site, signedIn bool, member func() bool, now time.Time) bool {
	if !b.IsActive || (b.ActiveFrom != nil && b.ActiveFrom.After(now)) || (b.ActiveTo != nil && !b.ActiveTo.After(now)) {
		return false
	}
	if b.ScopeEventID != nil {
		if site.EventID == nil || *b.ScopeEventID != *site.EventID {
			return false
		}
	} else if b.TargetMode != siteBannerModel.TargetAll {
		if b.TargetMode != siteBannerModel.TargetSelected {
			return false
		}
		if !slices.Contains(b.TargetSites, site.Kind) && !(site.EventID != nil && slices.Contains(b.TargetEventIDs, *site.EventID)) {
			return false
		}
	}
	switch b.Audience {
	case siteBannerModel.AudienceEveryone:
		return true
	case siteBannerModel.AudienceSignedIn:
		return signedIn
	case siteBannerModel.AudienceParticipants:
		return signedIn && b.ScopeEventID != nil && member()
	}
	return false
}
