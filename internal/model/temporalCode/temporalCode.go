package temporalCodeModel

import (
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Code is a single-use opaque temporal code (password reset / email change),
// carrying its typed JSON payload and a TTL. Data stays opaque bytes at this
// layer; callers marshal/unmarshal the concrete *CodeData payloads.
type Code struct {
	ID        uuid.UUID
	Code      string
	Type      int32
	Data      []byte
	ExpiresAt time.Time
}

// NewCode builds a temporal code for storage. now/ttl are resolved by the caller.
func NewCode(id uuid.UUID, code string, codeType int32, data []byte, expiresAt time.Time) Code {
	return Code{ID: id, Code: code, Type: codeType, Data: data, ExpiresAt: expiresAt}
}

// TemporalPasswordResettingCodeData is the JSON payload stored with a
// password-reset temporal code.
type TemporalPasswordResettingCodeData struct {
	UserID uuid.UUID
}

// TemporalEmailChangeCodeData is the JSON payload stored with an email-change code.
type TemporalEmailChangeCodeData struct {
	UserID uuid.UUID
	Email  string
}

// TemporalSetupLinkCodeData is the JSON payload stored with a setup-link code.
type TemporalSetupLinkCodeData struct {
	UserID uuid.UUID
}

// temporal code types
const (
	PasswordResettingCodeType = int32(iota)
	EmailChangeCodeType
	// SetupLinkCodeType keeps the id of a setup link (sign-up, invitation): the link works only
	// while its row exists, a new link for the same user replaces the old one, finishing the
	// setup deletes it.
	SetupLinkCodeType
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`.
//
// TemporalCodeObjectCode — next free detail code: 3
var (
	// multi-site (category A, security-indistinguishable): covers every way a
	// submitted code can be bad — undecodable, wrong type, and used-or-never-
	// existed. The client must not be able to tell these apart; per-site
	// reason via WithError. Expiry stays separate (ErrTemporalCodeExpired):
	// "request a new code" is deliberate UX, and 256-bit random codes make
	// the existence oracle worthless anyway.
	ErrTemporalCodeInvalidCode = err.ErrInvalidData.WithObjectCode(model.TemporalCodeObjectCode).
					WithMessage("Invalid code").WithDetailCode(1)
	ErrTemporalCodeExpired = err.ErrInvalidData.WithObjectCode(model.TemporalCodeObjectCode).
				WithMessage("Code expired").WithDetailCode(2)
)
