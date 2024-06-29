package model

import "github.com/gofrs/uuid"

const (
	// temporal code types

	EmailConfirmationCodeType = int32(iota)
	PasswordResettingCodeType
	ContinueRegistrationCodeType
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
