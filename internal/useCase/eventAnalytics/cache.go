package eventAnalytics

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/useCase/analyticsCache"
)

// Analytics reports are shared by every staff viewer of an event, and a
// dashboard polls them: see analyticsCache. reportTTL bounds how stale a
// report may be (§5: 5–30 s).
const reportTTL = 10 * time.Second

type reportCache = analyticsCache.Cache

func newReportCache(now func() time.Time) *reportCache {
	return analyticsCache.New(now, reportTTL)
}

// cachedReportOf: section use cases call it with a key that holds the
// section, the event and every filter.
func cachedReportOf[T any](ctx context.Context, c *reportCache, key string, load func(context.Context) (T, error)) (T, error) {
	return analyticsCache.Of(ctx, c, key, load)
}
