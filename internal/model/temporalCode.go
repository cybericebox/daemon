package model

import (
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/gofrs/uuid"
	"net/http"
)

type (
	TemporalEmailConfirmationCodeData struct {
		UserID uuid.UUID
		Email  string
	}

	TemporalPasswordResettingCodeData struct {
		UserID uuid.UUID
	}

	TemporalContinueRegistrationCodeData struct {
		Email string
		Role  string
	}
)

// temporal code types
const (
	EmailConfirmationCodeType = int32(iota)
	PasswordResettingCodeType
	ContinueRegistrationCodeType
)

// errors for temporal code
var (
	ErrInvalidTemporalCode = appError.NewError().WithCode(appError.CodeInvalidInput.
		WithMessage("invalid temporal code").
		WithHTTPCode(http.StatusUnauthorized))
)
