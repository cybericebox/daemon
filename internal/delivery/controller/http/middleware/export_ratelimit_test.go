package middleware

import "time"

// SetRateLimiterClock lets tests drive the token bucket's clock.
func SetRateLimiterClock(l *RateLimiter, now func() time.Time) { l.now = now }
