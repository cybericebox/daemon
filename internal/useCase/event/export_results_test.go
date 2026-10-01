package event

import "time"

// SetResultsCacheClock lets tests move the results cache's clock.
func SetResultsCacheClock(u *EventUseCase, now func() time.Time) { u.resultsCache.now = now }
