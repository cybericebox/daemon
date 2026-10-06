// Package analyticsCache is the shared report cache of the analytics use
// cases (event and platform): a report is computed once per key for
// concurrent viewers (singleflight) and kept for a short time (the results
// cache pattern, see useCase/event/results_cache.go).
package analyticsCache

import (
	"context"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

const (
	// idleTTL drops reports nobody reads any more.
	idleTTL = 5 * time.Minute
	// loadTimeout limits a shared load, which outlives the request that
	// started it.
	loadTimeout = 20 * time.Second
)

type entry struct {
	value    any
	loadedAt time.Time
}

// Cache holds reports under a key that carries the section and every filter.
type Cache struct {
	mu      sync.Mutex
	reports map[string]entry
	group   singleflight.Group
	now     func() time.Time
	ttl     time.Duration
}

// New makes a cache whose reports go stale after ttl.
func New(now func() time.Time, ttl time.Duration) *Cache {
	return &Cache{reports: map[string]entry{}, now: now, ttl: ttl}
}

// Get returns the report under key, loading it at most once for concurrent
// callers when it is missing or older than the TTL. The load is detached from
// the caller that started it, so one closed tab does not fail the others. A
// failed load is not cached.
func (c *Cache) Get(ctx context.Context, key string, load func(context.Context) (any, error)) (any, error) {
	if value, ok := c.fresh(key); ok {
		return value, nil
	}
	value, err, _ := c.group.Do(key, func() (any, error) {
		// A flight that just finished may already have refreshed it.
		if value, ok := c.fresh(key); ok {
			return value, nil
		}
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), loadTimeout)
		defer cancel()
		value, err := load(loadCtx)
		if err != nil {
			return nil, err
		}
		c.store(key, value)
		return value, nil
	})
	return value, err
}

func (c *Cache) fresh(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	held, ok := c.reports[key]
	if !ok || c.now().Sub(held.loadedAt) >= c.ttl {
		return nil, false
	}
	return held.value, true
}

func (c *Cache) store(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, held := range c.reports {
		if now.Sub(held.loadedAt) > idleTTL {
			delete(c.reports, k)
		}
	}
	c.reports[key] = entry{value: value, loadedAt: now}
}

// Of is the typed form of Get: section use cases call it with a key that
// holds the section and every filter.
func Of[T any](ctx context.Context, c *Cache, key string, load func(context.Context) (T, error)) (T, error) {
	value, err := c.Get(ctx, key, func(ctx context.Context) (any, error) { return load(ctx) })
	if err != nil {
		var zero T
		return zero, err
	}
	return value.(T), nil
}
