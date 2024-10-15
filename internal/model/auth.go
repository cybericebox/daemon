package model

import (
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/gofrs/uuid"
)

// models for token

type (
	Tokens struct {
		AccessToken      string
		RefreshToken     string
		PermissionsToken string
	}

	CheckTokensResult struct {
		Tokens    *Tokens
		UserID    uuid.UUID
		Refreshed bool
		Valid     bool
	}
)

// errors for token

var (
	ErrAuth                          = appError.ErrInternal.WithObjectCode(authObjectCode)
	ErrAuthInvalidUserCredentials    = appError.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(1).WithMessage("Invalid user credentials")
	ErrAuthInvalidOldPassword        = appError.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(2).WithMessage("Invalid old password")
	ErrAuthInvalidPasswordComplexity = appError.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(3).WithMessage("Invalid password complexity")
	ErrAuthInvalidOAuth2State        = appError.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(4).WithMessage("Invalid OAuth2 state")
	ErrAuthInvalidAccessToken        = appError.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(5).WithMessage("Invalid access token")
	ErrAuthInvalidRefreshToken       = appError.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(6).WithMessage("Invalid refresh token")

	ErrAuthRecaptcha                       = appError.ErrInternal.WithObjectCode(authRecaptchaObjectCode)
	ErrAuthRecaptchaInvalidRecaptchaToken  = appError.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(1).WithMessage("Invalid recaptcha token")
	ErrAuthRecaptchaNoRecaptchaToken       = appError.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(2).WithMessage("No recaptcha token")
	ErrAuthRecaptchaInvalidRecaptchaAction = appError.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(3).WithMessage("Invalid recaptcha action")
	ErrAuthRecaptchaLowerScore             = appError.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(4).WithMessage("Lower recaptcha score")
)

// constants for token
const (
	AccessToken      = "accessToken"
	RefreshToken     = "refreshToken"
	PermissionsToken = "permissionsToken"
)
