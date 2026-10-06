package userModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`.
//
// UserObjectCode — next free detail code: 5
var (
	ErrUserNotFound = err.ErrObjectNotFound.WithObjectCode(model.UserObjectCode).
			WithMessage("User not found").WithDetailCode(1)
	// multi-site: every "this email/account is already taken" outcome across
	// google-registration, google-link, invite and email-change flows —
	// deliberately one public error (the endpoint already tells the client
	// which flow it is); per-site reason via WithError.
	// DetailCode 2 — was 1, which collided with ErrUserNotFound (see err.As:
	// only DetailCode is compared, so errors.Is conflated the pair).
	ErrUserExists = err.ErrInvalidData.WithObjectCode(model.UserObjectCode).
			WithMessage("User already exists").WithDetailCode(2)
	// ErrUserModified: the optimistic-lock guard on the aggregate UPDATE hit 0
	// rows while the row still exists — someone else wrote between our read
	// and write. 409: the client should reload and retry.
	ErrUserModified = err.ErrConflict.WithObjectCode(model.UserObjectCode).
			WithMessage("User was modified concurrently").WithDetailCode(3)
	// ErrUserNameInvalid: a first or last name holds a control, bidirectional or zero-width character, or is
	// too long; such names let one person pass for another.
	ErrUserNameInvalid = err.ErrInvalidData.WithObjectCode(model.UserObjectCode).
				WithMessage("The name contains characters that are not allowed").WithDetailCode(4)
)
