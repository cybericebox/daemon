package model

import (
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/gofrs/uuid"
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
	ErrTemporalCode            = appError.ErrInternal.WithObjectCode(temporalCoreObjectCode)
	ErrTemporalCodeInvalidCode = appError.ErrInvalidData.WithObjectCode(temporalCoreObjectCode).WithDetailCode(1).WithMessage("Invalid code")
	ErrTemporalCodeNotFound    = appError.ErrObjectNotFound.WithObjectCode(temporalCoreObjectCode).WithDetailCode(2).WithMessage("Code not found")
)
