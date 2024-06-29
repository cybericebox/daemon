package model

import (
	"github.com/cybericebox/daemon/internal/tools"
	"net/http"
)

// models for token

type Tokens struct {
	AccessToken      string
	RefreshToken     string
	PermissionsToken string
}

// errors for token

var (
	ErrInvalidUserCredentials    = tools.NewError("invalid user credentials", http.StatusBadRequest)
	ErrInvalidTemporalCode       = tools.NewError("invalid temporal code", http.StatusBadRequest)
	ErrInvalidOldPassword        = tools.NewError("invalid old password", http.StatusBadRequest)
	ErrInvalidPasswordComplexity = tools.NewError("invalid password complexity", http.StatusBadRequest)
)

// constants for token
const (
	AccessToken      = "accessToken"
	RefreshToken     = "refreshToken"
	PermissionsToken = "permissionsToken"
)
