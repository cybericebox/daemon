package challengeAttempt

import (
	"math"
	"time"

	"github.com/cybericebox/daemon/pkg/err"
)

// RateLimit bounds flag submissions in a sliding window: at most Attempts
// within any Window. Counting happens in Postgres, so the limit holds across
// every backend replica.
type RateLimit struct {
	Attempts int32
	Window   time.Duration
}

var (
	// ChallengeRateLimit applies per team and challenge (and per moderators
	// board challenge): enough for typos, too slow for guessing.
	ChallengeRateLimit = RateLimit{Attempts: 5, Window: 30 * time.Second}
	// TeamRateLimit is the looser limit over all challenges of one team.
	TeamRateLimit = RateLimit{Attempts: 20, Window: time.Minute}
)

// Since returns the start of the window that ends at now.
func (l RateLimit) Since(now time.Time) time.Time {
	return now.Add(-l.Window)
}

// Check takes the latest attempts inside the window (at most l.Attempts) and
// the oldest of them. At the limit the next attempt is allowed once that
// oldest one leaves the window; the delay is rounded up to whole seconds.
func (l RateLimit) Check(recent int64, oldest, now time.Time) error {
	if recent < int64(l.Attempts) {
		return nil
	}
	seconds := int64(math.Ceil(oldest.Add(l.Window).Sub(now).Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return ErrTooManyAttempts.WithDetail(err.DetailRetryAfterSeconds, seconds).Err()
}
