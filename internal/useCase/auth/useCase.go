package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

const fakeHashedPassword = "$2a$10$dyXylZqNUe.KbtN.TSN8kuX7LcHju9kxh0HlC9AdvO3sSM8qrevNW" // just for imitation of hashed password

type (
	AuthUseCase struct {
		service IAuthService
	}

	IAuthService interface {
		IEmailService
		IGoogleService
		ISignUpService
		IPasswordService

		GetUserByEmail(ctx context.Context, email string) (*model.User, error)
		GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error)

		Matches(password, hashedPassword string) (bool, error)

		ValidateAccessToken(ctx context.Context, accessToken string) (uuid.UUID, bool)
		RefreshTokens(refreshToken string) (*model.Tokens, error)
		GenerateTokens(subject string) (*model.Tokens, error)
	}

	Dependencies struct {
		Service IAuthService
	}
)

func NewUseCase(deps Dependencies) *AuthUseCase {
	return &AuthUseCase{
		service: deps.Service,
	}

}

func (u *AuthUseCase) SignIn(ctx context.Context, email, password string) (*model.Tokens, error) {
	user, err := u.service.GetUserByEmail(ctx, email)
	// if error is not nil and error is not ErrNotFound return error
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, err
	}
	// if user not found emulate password check and after return invalid user credentials error
	if errors.Is(err, model.ErrNotFound) {
		_, err = u.service.Matches(password, fakeHashedPassword)
		if err != nil {
			return nil, err
		}
		return nil, model.ErrInvalidUserCredentials
	}

	// if user has password, check it
	matches, err := u.service.Matches(password, user.HashedPassword)
	if err != nil {
		return nil, err
	}

	if !matches {
		return nil, model.ErrInvalidUserCredentials
	}

	// if password is correct generate tokens and return them
	return u.service.GenerateTokens(user.ID.String())
}

func (u *AuthUseCase) RefreshTokensIfNeedAndReturnUserID(ctx context.Context, oldTokens model.Tokens) (*model.Tokens, *uuid.UUID, bool, bool) {
	userID, valid := u.service.ValidateAccessToken(ctx, oldTokens.AccessToken)

	if valid {
		return &oldTokens, &userID, true, false
	}
	// if access token is not valid, try to refresh tokens
	tokens, err := u.service.RefreshTokens(oldTokens.RefreshToken)
	if err != nil {
		return nil, nil, false, false
	}

	return tokens, &userID, true, true
}

func (u *AuthUseCase) GetSelfProfile(ctx context.Context) (*model.UserInfo, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return nil, err
	}

	return &model.UserInfo{
		ID:       user.ID,
		Email:    user.Email,
		Name:     user.Name,
		Picture:  user.Picture,
		Role:     user.Role,
		LastSeen: user.LastSeen,
	}, nil
}

func (u *AuthUseCase) URLNeedsProtection(ctx context.Context, url string) bool {
	// get subdomain from context
	subdomain, err := tools.GetSubdomainFromContext(ctx)
	if err != nil {
		return true
	}

	return url == "/profile" || url == "/challenges" || subdomain == config.AdminSubdomain
}
