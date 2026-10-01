// Application-layer read models (views): shapes produced by this use case for
// the delivery layer. They are not domain entities — the domain package keeps
// only entities, value objects, and domain errors.
package auth

import (
	"time"

	"github.com/gofrs/uuid"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// SessionAuthResult is returned by ValidateSessionCookie.
type SessionAuthResult struct {
	Claims  authModel.AuthClaims
	Session *authModel.Session
}

// SessionInfo is the account "Sessions" view of one active session.
type SessionInfo struct {
	ID        uuid.UUID
	UserAgent string
	IP        string
	LastSeen  time.Time
	IsCurrent bool
	CreatedAt time.Time
}

// SetupContext is the registration-completion screen state resolved from a setup token.
type SetupContext struct {
	Email       string
	FirstName   string
	LastName    string
	HasProvider bool
}

// AccountInfo is the signed-in user's account view.
type AccountInfo struct {
	FirstName      string
	LastName       string
	Email          string
	Picture        string
	EmailConfirmed bool
	Role           rbac.Role
	Providers      []string
	HasPassword    bool
	CreatedAt      time.Time
}

// PasswordPolicy is the public view of the active password complexity rules.
// SpecialCharacters lists exactly the characters counted towards
// MinSpecialCharacters.
type PasswordPolicy struct {
	MinLength            int
	MaxLength            int
	MinCapitalLetters    int
	MinSmallLetters      int
	MinDigits            int
	MinSpecialCharacters int
	SpecialCharacters    string
}

// GoogleRegistrationResult is the outcome of BeginGoogleRegistration. Exactly
// one of SetupToken / SessionCookie is set: SetupToken continues registration
// at /setup (ReturnTo: the trusted return_to to keep on it, may be empty);
// SessionCookie (with Redirect, the landing URL) signs in an account that is
// already registered with this Google identity.
type GoogleRegistrationResult struct {
	SetupToken    string
	ReturnTo      string
	SessionCookie string
	Redirect      string
}
