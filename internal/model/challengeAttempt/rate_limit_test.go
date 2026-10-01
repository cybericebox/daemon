package challengeAttempt_test

import (
	"errors"
	"net/http"
	"testing"
	"time"

	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	"github.com/cybericebox/daemon/pkg/err"
)

func TestRateLimitSince(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	limit := challengeAttempt.RateLimit{Attempts: 5, Window: 30 * time.Second}
	if got := limit.Since(now); !got.Equal(now.Add(-30 * time.Second)) {
		t.Fatalf("Since = %v", got)
	}
}

func TestRateLimitCheck_AllowsBelowLimit(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	limit := challengeAttempt.RateLimit{Attempts: 5, Window: 30 * time.Second}
	if e := limit.Check(4, now.Add(-10*time.Second), now); e != nil {
		t.Fatalf("Check below limit = %v", e)
	}
}

func TestRateLimitCheck_RejectsAtLimitUntilOldestLeavesWindow(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	limit := challengeAttempt.RateLimit{Attempts: 5, Window: 30 * time.Second}
	e := limit.Check(5, now.Add(-12*time.Second-300*time.Millisecond), now)
	if !errors.Is(e, challengeAttempt.ErrTooManyAttempts.Err()) {
		t.Fatalf("Check at limit = %v", e)
	}
	var appErr err.Error
	if !errors.As(e, &appErr) {
		t.Fatalf("not an app error: %v", e)
	}
	code := appErr.StatusCode()
	if code.HTTPCode() != http.StatusTooManyRequests {
		t.Fatalf("HTTP code = %d", code.HTTPCode())
	}
	// 17.7 s remain: rounded up so a client retrying on time is let through.
	if got := code.Details()[err.DetailRetryAfterSeconds]; got != int64(18) {
		t.Fatalf("retry after = %v", got)
	}
}

func TestRateLimitCheck_WaitsAtLeastOneSecond(t *testing.T) {
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	limit := challengeAttempt.RateLimit{Attempts: 1, Window: 30 * time.Second}
	e := limit.Check(1, now.Add(-30*time.Second), now)
	var appErr err.Error
	if !errors.As(e, &appErr) || appErr.StatusCode().Details()[err.DetailRetryAfterSeconds] != int64(1) {
		t.Fatalf("Check = %v", e)
	}
}

func TestDefaultRateLimits(t *testing.T) {
	if challengeAttempt.ChallengeRateLimit.Attempts != 5 || challengeAttempt.ChallengeRateLimit.Window != 30*time.Second {
		t.Fatalf("challenge limit = %+v", challengeAttempt.ChallengeRateLimit)
	}
	if challengeAttempt.TeamRateLimit.Attempts != 20 || challengeAttempt.TeamRateLimit.Window != time.Minute {
		t.Fatalf("team limit = %+v", challengeAttempt.TeamRateLimit)
	}
}
