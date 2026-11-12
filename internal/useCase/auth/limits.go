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
	mailKindSignUp = "sign-up"
	mailKindReset  = "password-reset"
)

type authLimits struct {
	signInAccount    *ratelimit.Lockout // by normalized address
	passwordGuess    *ratelimit.Lockout // by user id: old-password / current-password checks
	mail             *ratelimit.Recipient
	mailRequests     *ratelimit.Recipient // by normalized address, every kind of sign-in mail together
	emailChangeUsers *ratelimit.Window
	dataExport       *ratelimit.Window // by user id
}

func newAuthLimits() *authLimits {
	c := limits.Get()
	return &authLimits{
		signInAccount: ratelimit.NewLockout(
			c.SignInMaxFailures,
			c.SignInFailureWindow,
			c.SignInLockBase,
			c.SignInLockMax,
		),
		passwordGuess: ratelimit.NewLockout(
			c.SignInMaxFailures,
			c.SignInFailureWindow,
			c.SignInLockBase,
			c.SignInLockMax,
		),
		mail:             ratelimit.NewRecipient(c.AccountMailGap, c.AccountMailPerHour),
		mailRequests:     ratelimit.NewRecipient(c.AccountMailGap, c.AccountMailPerHour),
		emailChangeUsers: ratelimit.NewWindow(c.EmailChangesPerHour, time.Hour),
		dataExport:       ratelimit.NewWindow(1, dataExportInterval),
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

// requestMail is the front door of every request that can make the platform mail an address a sign-in message
// (sign-up, password reset, email change, and their resends): at most one in AccountMailGap and AccountMailPerHour
// an hour, counted for all kinds together. It runs before any account lookup and is keyed on the address alone,
// so the refusal (429 with RetryAfterSeconds) says nothing about whether an account exists.
//
// The returned release gives the request back when what followed failed before anything was accepted.
func (u *AuthUseCase) requestMail(address string) (func(), error) {
	ok, wait, release := u.limits.mailRequests.Take(normalizeEmail(address))
	if !ok {
		return nil, tooManyRequests(wait)
	}
	return release, nil
}
