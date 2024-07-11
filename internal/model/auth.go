package model

import (
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/gofrs/uuid"
	"net/http"
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
		Valid     bool
		Refreshed bool
	}
)

// errors for token

var (
	ErrInvalidUserCredentials = appError.NewError().WithCode(appError.CodeInvalidInput.
					WithMessage("invalid user credentials").
					WithHTTPCode(http.StatusUnauthorized))
	ErrInvalidOldPassword = appError.NewError().WithCode(appError.CodeInvalidInput.
				WithMessage("invalid old password").
				WithHTTPCode(http.StatusUnauthorized))
	ErrInvalidPasswordComplexity = appError.NewError().WithCode(appError.CodeInvalidInput.
					WithMessage("invalid password complexity"))
)

// constants for token
const (
	AccessToken      = "accessToken"
	RefreshToken     = "refreshToken"
	PermissionsToken = "permissionsToken"
)
