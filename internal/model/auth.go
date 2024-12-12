package model

import (
	"github.com/cybericebox/lib/pkg/err"
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
	ErrAuth                          = err.ErrInternal.WithObjectCode(authObjectCode)
	ErrAuthInvalidUserCredentials    = err.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(1).WithMessage("Invalid user credentials")    //20701
	ErrAuthInvalidOldPassword        = err.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(2).WithMessage("Invalid old password")        //20702
	ErrAuthInvalidPasswordComplexity = err.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(3).WithMessage("Invalid password complexity") //20703
	ErrAuthInvalidOAuth2State        = err.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(4).WithMessage("Invalid OAuth2 state")        //20704
	ErrAuthInvalidAccessToken        = err.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(5).WithMessage("Invalid access token")        //20705
	ErrAuthInvalidRefreshToken       = err.ErrInvalidData.WithObjectCode(authObjectCode).WithDetailCode(6).WithMessage("Invalid refresh token")       // 20706

	ErrAuthRecaptcha                       = err.ErrInternal.WithObjectCode(authRecaptchaObjectCode)
	ErrAuthRecaptchaInvalidRecaptchaToken  = err.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(1).WithMessage("Invalid recaptcha token")  //20801
	ErrAuthRecaptchaNoRecaptchaToken       = err.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(2).WithMessage("No recaptcha token")       //20802
	ErrAuthRecaptchaInvalidRecaptchaAction = err.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(3).WithMessage("Invalid recaptcha action") //20803
	ErrAuthRecaptchaLowerScore             = err.ErrInvalidData.WithObjectCode(authRecaptchaObjectCode).WithDetailCode(4).WithMessage("Lower recaptcha score")    //20804
)

// constants for token
const (
	AccessToken      = "accessToken"
	RefreshToken     = "refreshToken"
	PermissionsToken = "permissionsToken"
)
