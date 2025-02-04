package authModel

import (
	"github.com/cybericebox/daemon/internal/model"
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
	ErrAuth                          = err.ErrInternal.WithObjectCode(model.AuthObjectCode)
	ErrAuthInvalidUserCredentials    = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).WithDetailCode(1).WithMessage("Invalid user credentials")    //20701
	ErrAuthInvalidOldPassword        = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).WithDetailCode(2).WithMessage("Invalid old password")        //20702
	ErrAuthInvalidPasswordComplexity = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).WithDetailCode(3).WithMessage("Invalid password complexity") //20703
	ErrAuthInvalidOAuth2State        = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).WithDetailCode(4).WithMessage("Invalid OAuth2 state")        //20704
	ErrAuthInvalidAccessToken        = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).WithDetailCode(5).WithMessage("Invalid access token")        //20705
	ErrAuthInvalidRefreshToken       = err.ErrInvalidData.WithObjectCode(model.AuthObjectCode).WithDetailCode(6).WithMessage("Invalid refresh token")       // 20706
)

// constants for token
const (
	AccessToken      = "accessToken"
	RefreshToken     = "refreshToken"
	PermissionsToken = "permissionsToken"
)
