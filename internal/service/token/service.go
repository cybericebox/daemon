package token

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/token"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"time"
)

type (
	TokenService struct {
		config       *config.JWTConfig
		repository   IRepository
		tokenManager tokenManager
	}
	IRepository interface {
		DoesUserExistByID(ctx context.Context, id uuid.UUID) (bool, error)

		SetLastSeen(ctx context.Context, id uuid.UUID) error
	}

	tokenManager interface {
		NewAccessToken(subject interface{}, ttl ...time.Duration) (string, error)
		NewRefreshToken(subject interface{}, ttl ...time.Duration) (string, error)
		ParseAccessToken(token string) (interface{}, error)
		ParseRefreshToken(token string) (interface{}, error)
	}

	Dependencies struct {
		Config     *config.JWTConfig
		Repository IRepository
	}
)

func NewTokenService(deps Dependencies) *TokenService {
	manager, err := token.NewTokenManager(deps.Config.TokenSignature, deps.Config.AccessTokenTTL, deps.Config.RefreshTokenTTL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create token manager")
	}
	return &TokenService{
		config:       deps.Config,
		repository:   deps.Repository,
		tokenManager: manager,
	}
}

func (s *TokenService) ValidateAccessToken(ctx context.Context, accessToken string) (uuid.UUID, error) {
	userID, err := s.tokenManager.ParseAccessToken(accessToken)
	if err != nil {
		return uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to parse access token")
	}

	userIDParsed, err := uuid.FromString(userID.(string))
	if err != nil {
		return uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to parse user id")
	}

	exists, err := s.repository.DoesUserExistByID(ctx, userIDParsed)
	if err != nil {
		return uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to check if user exists")
	}

	if !exists {
		return uuid.Nil, appError.NewError().WithCode(appError.CodeNotFound)
	}

	// set last seen time
	if err = s.repository.SetLastSeen(ctx, userIDParsed); err != nil {
		return uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to set last seen")
	}

	return userIDParsed, nil
}

func (s *TokenService) RefreshTokens(refreshToken string) (*model.Tokens, uuid.UUID, error) {
	subject, err := s.tokenManager.ParseRefreshToken(refreshToken)
	if err != nil {
		return nil, uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to parse refresh token")
	}

	userIDParsed, err := uuid.FromString(subject.(string))
	if err != nil {
		return nil, uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to parse user id")
	}

	tokens, err := s.GenerateTokens(subject.(string))
	if err != nil {
		return nil, uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to generate tokens")
	}

	return tokens, userIDParsed, nil
}

func (s *TokenService) GenerateTokens(subject string) (*model.Tokens, error) {
	tokens := model.Tokens{}
	var err error

	tokens.AccessToken, err = s.tokenManager.NewAccessToken(subject)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to generate access token")
	}

	tokens.RefreshToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to generate refresh token")
	}

	tokens.PermissionsToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to generate permissions token")
	}

	return &tokens, nil
}
