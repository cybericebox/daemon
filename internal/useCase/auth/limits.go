package auth

import (
	"math"
	"time"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/ratelimit"
)

// Thresholds of the auth abuse limits. They are generous on purpose: a whole
// computer lab signs in from one address at the start of an event, so the
// per-client limits count FAILURES only, and success never costs quota.
const (
	// accountMaxFailures wrong passwords for one address (or one signed-in
	// user's old-password checks) lock it, 1 min doubling to 15 min.
	accountMaxFailures = 5
	failureWindow      = 15 * time.Minute
	accountBaseLock    = time.Minute
	accountMaxLock     = 15 * time.Minute
	// clientMaxFailures wrong passwords from one client address lock it, 1 min doubling to 15 min.
	clientMaxFailures = 40

	// mailGap is the least time between two mails of one kind to one address;
	// mailPerHour caps them per kind and hour.
	mailGap     = time.Minute
	mailPerHour = 3
	// emailChangesPerUserHour bounds the email-change confirmations one
	// account can make the platform send (to any addresses).
	emailChangesPerUserHour = 5
)

// Mail kinds are counted separately per recipient, so a flood of one kind
// cannot use up the quota of an unrelated one.
const (
	mailKindSignUp      = "sign-up"
	mailKindReset       = "password-reset"
	mailKindEmailChange = "email-change"
	mailKindInvite      = "invite"
)

type authLimits struct {
	signInAccount    *ratelimit.Lockout // by normalized address
	signInClient     *ratelimit.Lockout // by client address
	passwordGuess    *ratelimit.Lockout // by user id: old-password / current-password checks
	mail             *ratelimit.Recipient
	emailChangeUsers *ratelimit.Window
}

func newAuthLimits() *authLimits {
	return &authLimits{
		signInAccount:    ratelimit.NewLockout(accountMaxFailures, failureWindow, accountBaseLock, accountMaxLock),
		signInClient:     ratelimit.NewLockout(clientMaxFailures, failureWindow, accountBaseLock, accountMaxLock),
		passwordGuess:    ratelimit.NewLockout(accountMaxFailures, failureWindow, accountBaseLock, accountMaxLock),
		mail:             ratelimit.NewRecipient(mailGap, mailPerHour),
		emailChangeUsers: ratelimit.NewWindow(emailChangesPerUserHour, time.Hour),
	}
}

// signInWait is how long a sign-in for account from client is refused. An empty
// client address (outside HTTP) must not pool everyone into one key.
func (l *authLimits) signInWait(account, client string) time.Duration {
	wait := l.signInAccount.Locked(account)
	if client != "" {
		wait = max(wait, l.signInClient.Locked(client))
	}
	return wait
}

func (l *authLimits) signInFailed(account, client string) {
	l.signInAccount.Fail(account)
	if client != "" {
		l.signInClient.Fail(client)
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
