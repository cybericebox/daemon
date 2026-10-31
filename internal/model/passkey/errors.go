package passkeyModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by `make lint-errors`.
//
// PasskeyObjectCode — next free detail code: 7.
//
//nolint:gochecknoglobals,mnd // the error registry: one var per code, parsed by tools/errorcatalog
var (
	// ErrPasskeySignInFailed is multi-site (category A, security-indistinguishable): every way a passkey sign-in
	// can fail (unknown key, a key of another person, a bad signature, a missing user verification, a foreign
	// origin, a replayed counter, a deleted account). The client must not tell them apart; the reason goes to
	// the logs via WithError.
	ErrPasskeySignInFailed = err.ErrUnauthenticated.WithObjectCode(model.PasskeyObjectCode).
				WithMessage("Passkey sign-in failed").WithDetailCode(1)
	// ErrPasskeyCeremonyInvalid is multi-site (category A): a ceremony that cannot be continued because it is
	// unknown, used, expired, or belongs to another person or session. The person simply starts again.
	ErrPasskeyCeremonyInvalid = err.ErrInvalidData.WithObjectCode(model.PasskeyObjectCode).
					WithMessage("Passkey request expired, try again").WithDetailCode(2)
	ErrPasskeyLimit = err.ErrInvalidData.WithObjectCode(model.PasskeyObjectCode).
			WithMessage("Passkey limit reached").WithDetailCode(3)
	ErrPasskeyNotFound = err.ErrObjectNotFound.WithObjectCode(model.PasskeyObjectCode).
				WithMessage("Passkey not found").WithDetailCode(4)
	// ErrPasskeyRegistrationFailed is multi-site: the authenticator's registration response did not pass
	// verification (wrong origin, no user verification, a key already registered, a malformed response); the
	// reason goes to the logs via WithError.
	ErrPasskeyRegistrationFailed = err.ErrInvalidData.WithObjectCode(model.PasskeyObjectCode).
					WithMessage("Passkey registration failed").WithDetailCode(5)
	ErrPasskeyInvalidName = err.ErrInvalidData.WithObjectCode(model.PasskeyObjectCode).
				WithMessage("Invalid passkey name").WithDetailCode(6)
)
