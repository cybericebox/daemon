// Package platformAnalytics is the application layer of platform-level
// analytics (docs/EVENT-ANALYTICS.md §8, docs/PLATFORM-ANALYTICS.md): one
// read model per admin «Аналітика» section, aggregates only. Every report is
// loaded through the shared cache (analyticsCache) under a key that holds the
// section and every filter.
package platformAnalytics

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/model"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/internal/useCase/analyticsCache"
)

// reportTTL bounds how stale a report may be; the admin polls the live
// sections (overview, infrastructure) about every 30 s.
const reportTTL = 15 * time.Second

type (
	// Store reads the aggregates; one port per section, satisfied by
	// *platformAnalyticsRepo.Repository.
	Store interface {
		OverviewStore
		UsersStore
		EventsStore
		TasksStore
		InfrastructureStore
		InfrastructureKindStore
		MailStore
	}

	Dependencies struct {
		Store Store
		// Now is the clock; nil means time.Now.
		Now func() time.Time
		// TestLabResources sums what the catalog test labs use now; nil leaves it unknown.
		TestLabResources func(context.Context) (labMonitoringModel.Resources, error)
	}

	PlatformAnalyticsUseCase struct {
		store            Store
		testLabResources func(context.Context) (labMonitoringModel.Resources, error)
		cache            *analyticsCache.Cache
		now              func() time.Time
	}
)

func New(deps Dependencies) *PlatformAnalyticsUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &PlatformAnalyticsUseCase{store: deps.Store, testLabResources: deps.TestLabResources, cache: analyticsCache.New(now, reportTTL), now: now}
}

// period resolves the requested window; a missing `to` is now, a missing
// `from` is all time.
func (u *PlatformAnalyticsUseCase) period(from, to *time.Time) (platformAnalyticsModel.Period, error) {
	return platformAnalyticsModel.NewPeriod(from, to, u.now())
}

// cached loads a report through the shared cache: the key must hold the
// section and every filter (add period.Key()). load wraps its own errors with
// fail.
func cached[T any](ctx context.Context, u *PlatformAnalyticsUseCase, key string, load func(context.Context) (T, error)) (T, error) {
	return analyticsCache.Of(ctx, u.cache, key, load)
}

// fail wraps a store error as a platform error.
func fail(err error, message string) error {
	return model.ErrPlatform.WithError(err).WithMessage(message).Err()
}
