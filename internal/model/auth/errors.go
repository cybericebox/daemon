package authModel

import (
	"net/http"

	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention (enforced by `make lint-errors`):
//   - one Err* var = one call site in non-test code = unique DetailCode within
//     the object code; exceptions are documented as multi-site with a category:
//     category A — security-indistinguishable (client must not tell the cases
//     apart; per-site reason goes to logs via WithError);
//     category B — RBAC guard (always 403).
//   - the HTTP status comes from the base (ErrUnauthenticated→401,
//     ErrObjectNotFound→404, ...), so "the same fact" needing two statuses is
//     two different error vars by definition.
//
// AuthObjectCode — next free detail code: 30
var (
	// 401
	// multi-site (category A): sign-in must be timing- and error-
	// indistinguishable between "no such user" and "wrong password"
	// (anti-enumeration).
	ErrAuthInvalidUserCredentials = err.ErrUnauthenticated.WithObjectCode(model.AuthObjectCode).
					WithMessage("Invalid user credentials").WithDetailCode(1)
	ErrAuthSessionExpired = err.ErrUnauthenticated.WithObjectCode(model.AuthObjectCode).
				WithMessage("Session expired").WithDetailCode(2)
	// multi-site (category A, security-indistinguishable): covers every way a
	// presented session credential can be dead — unparseable cookie AND a
	// parseable cookie whose session row is gone (revoked/deleted). The client
	// must not be able to tell these apart (a distinct code/message would
	// confirm the session once existed); the internal reason travels via
	// WithError for server logs only.
	ErrAuthInvalidSession = err.ErrUnauthenticated.WithObjectCode(model.AuthObjectCode).
				WithMessage("Invalid session").WithDetailCode(3)
	// multi-site (category A): token parse failure and user-gone-behind-token
	// must not be tellable apart; per-site reason via WithError.
	ErrInvalidToken = err.ErrUnauthenticated.WithObjectCode(model.AuthObjectCode).
			WithMessage("Invalid token").WithDetailCode(4)
	// ErrAuthMissingSessionCookie: the request carried no session cookie at
	// all. Safe to distinguish from a failed validation — the client can see
	// its own request, so this reveals nothing (and helps debug cookie/CORS
	// misconfiguration).
	ErrAuthMissingSessionCookie = err.ErrUnauthenticated.WithObjectCode(model.AuthObjectCode).
					WithMessage("Session cookie is missing").WithDetailCode(22)

	// DetailCode 5 — was 3, which collided with ErrAuthInvalidSession: the lib's
	// err.As compares only DetailCode, so errors.Is conflated a 401 with a 404.
	ErrAuthSessionNotFound = err.ErrObjectNotFound.WithObjectCode(model.AuthObjectCode).
				WithMessage("Session not found").WithDetailCode(5)
	ErrSetupAlreadyComplete = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Setup already complete").WithDetailCode(6)
	ErrTosNotAccepted = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Terms of Service not accepted").WithDetailCode(7)
	// multi-site: registration and google-unlink both guard "account must keep
	// at least one login method"; per-site reason via WithError.
	ErrNoLoginMethod = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("At least one login method is required").WithDetailCode(8)
	ErrAuthInvalidPasswordComplexity = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
						WithMessage("Password does not meet complexity requirements").
						WithDetailCode(9)
	ErrAuthInvalidOldPassword = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
					WithMessage("Invalid old password").WithDetailCode(10)
	ErrAuthAccountExistsSignIn = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
					WithMessage("Account already exists, sign in").WithDetailCode(11)
	ErrAuthGoogleNotRegistered = err.ErrObjectNotFound.WithObjectCode(model.AuthObjectCode).
					WithMessage("Google account not registered").WithDetailCode(12)

	// multi-site (category B): the RBAC guard error — always 403, returned by
	// every permission check.
	ErrInsufficientPermission = err.ErrForbidden.WithObjectCode(model.AuthObjectCode).
					WithMessage("Insufficient permission").WithDetailCode(13)
	ErrCannotAssignRole = err.ErrForbidden.WithObjectCode(model.AuthObjectCode).
				WithMessage("Cannot assign role").WithDetailCode(14)
	ErrLastSuperAdmin = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Cannot remove the last super administrator").WithDetailCode(15)
	ErrInvalidRole = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
			WithMessage("Invalid role").WithDetailCode(16)
	// multi-site: input validation in the use case (fail fast before loading
	// the aggregate) and the transition guard on the entity (Block/Activate).
	ErrInvalidUserStatus = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Invalid user status").WithDetailCode(17)
	ErrAuthAccountBlocked = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Account is blocked").WithDetailCode(18)
	// multi-site: every avatar operation guards "object storage configured";
	// per-site reason via WithError.
	ErrStorageUnavailable = err.ErrInternal.WithObjectCode(model.AuthObjectCode).
				WithMessage("Storage unavailable").WithDetailCode(19)
	ErrInvalidAvatarType = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Unsupported image type").WithDetailCode(20)
	ErrAvatarTooLarge = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Avatar file too large").WithDetailCode(21)
	ErrInviteBatchSize = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Invite 1 to 200 people at a time").WithDetailCode(23)
	// ErrAuthGoogleEmailNotVerified: Google does not vouch for the address of
	// this profile, so it cannot prove mailbox ownership.
	ErrAuthGoogleEmailNotVerified = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
					WithMessage("Google email is not verified").WithDetailCode(24)
	// ErrAuthGoogleEmailMismatch: a Google identity may complete a setup only
	// when its verified email is the address of the account being set up.
	ErrAuthGoogleEmailMismatch = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
					WithMessage("Google email does not match the account").WithDetailCode(25)
	// ErrAuthPasswordRequired: the action re-checks the account password, and
	// this account has none (Google-only): set one first.
	ErrAuthPasswordRequired = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Set a password first").WithDetailCode(27)
	// ErrAuthTooManyRequests: a sign-in / credential-check lockout or a mail
	// quota was hit (HTTP 429); the Retry-After header carries the wait.
	// multi-site: every throttled auth action returns it.
	ErrAuthTooManyRequests = err.ErrConflict.WithObjectCode(model.AuthObjectCode).
				WithMessage("Too many attempts, try again later").WithDetailCode(28).
				WithHTTPCode(http.StatusTooManyRequests)
	// ErrAuthReauthRequired: a sensitive action of an account without a password (Google only) needs a
	// sign-in from the last minutes; the client sends the person through sign-in again.
	ErrAuthReauthRequired = err.ErrForbidden.WithObjectCode(model.AuthObjectCode).
				WithMessage("Sign in again to confirm this action").WithDetailCode(29)
	ErrAuthInvalidEmail = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).
				WithMessage("Invalid email address").WithDetailCode(26)
)

// AuthRecaptchaObjectCode — next free detail code: 5
var (
	ErrAuthInvalidRecaptchaToken = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).
					WithMessage("Invalid recaptcha token").WithDetailCode(1)
	ErrAuthNoRecaptchaToken = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).
				WithMessage("No recaptcha token").WithDetailCode(2)
	ErrAuthInvalidRecaptchaAction = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).
					WithMessage("Invalid recaptcha action").
					WithDetailCode(3)
	ErrAuthLowerScore = err.ErrInvalidData.WithObjectCode(model.AuthRecaptchaObjectCode).
				WithMessage("Lower recaptcha score").WithDetailCode(4)
)
