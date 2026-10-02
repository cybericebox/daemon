package auth

import (
	"math"
	"time"

	"github.com/cybericebox/daemon/internal/limits"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/ratelimit"
)

// The abuse limits of the auth flows (config.LimitsConfig, LIMIT_* env keys).
// Nothing is keyed on a client address: a whole computer lab signs in from one
// address at the start of an event. Failures are counted per account and
// success never costs quota.

// Mail kinds are counted separately per recipient, so a flood of one kind
// cannot use up the quota of an unrelated one. Only mail the account itself
// triggers is limited; invitations sent by an organizer or admin are not.
const (
	mailKindSignUp      = "sign-up"
	mailKindReset       = "password-reset"
	mailKindEmailChange = "email-change"
)

type authLimits struct {
	signInAccount    *ratelimit.Lockout // by normalized address
	passwordGuess    *ratelimit.Lockout // by user id: old-password / current-password checks
	mail             *ratelimit.Recipient
	emailChangeUsers *ratelimit.Window
	mailGap          time.Duration
}

func newAuthLimits() *authLimits {
	c := limits.Get()
	return &authLimits{
		signInAccount:    ratelimit.NewLockout(c.SignInMaxFailures, c.SignInFailureWindow, c.SignInLockBase, c.SignInLockMax),
		passwordGuess:    ratelimit.NewLockout(c.SignInMaxFailures, c.SignInFailureWindow, c.SignInLockBase, c.SignInLockMax),
		mail:             ratelimit.NewRecipient(c.AccountMailGap, c.AccountMailPerHour),
		emailChangeUsers: ratelimit.NewWindow(c.EmailChangesPerHour, time.Hour),
		mailGap:          c.AccountMailGap,
	}
}

// tooManyRequests is the 429 carrying the wait in Retry-After.
func tooManyRequests(wait time.Duration) error {
	seconds := int64(math.Ceil(wait.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	return authModel.ErrAuthTooManyRequests.WithDetail(err.DetailRetryAfterSeconds, seconds).Err()
}

// mailAllowed reports whether another mail of kind may go to address; callers
// that must not reveal anything stay silent when it is false.
func (u *AuthUseCase) mailAllowed(kind, address string) bool {
	return u.limits.mail.Allow(kind, normalizeEmail(address))
}
