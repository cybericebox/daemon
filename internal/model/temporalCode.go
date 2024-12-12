package model

import (
	"github.com/cybericebox/lib/pkg/err"
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
	ErrTemporalCode = err.ErrInternal.WithObjectCode(temporalCoreObjectCode)

	ErrTemporalCodeInvalidCode = err.ErrInvalidData.WithObjectCode(temporalCoreObjectCode).WithDetailCode(1).WithMessage("Invalid code") // 20601
	ErrTemporalCodeExpired     = err.ErrInvalidData.WithObjectCode(temporalCoreObjectCode).WithDetailCode(2).WithMessage("Code expired") // 20602

	ErrTemporalCodeNotFound = err.ErrObjectNotFound.WithObjectCode(temporalCoreObjectCode).WithDetailCode(1).WithMessage("Code not found") // 30601
)
