package token

import (
	"context"
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
		log.Fatal().Err(err).Msg("failed to create token manager")
	}
	return &TokenService{
		config:       deps.Config,
		repository:   deps.Repository,
		tokenManager: manager,
	}
}

func (s *TokenService) ValidateAccessToken(ctx context.Context, accessToken string) (uuid.UUID, bool) {
	userID, err := s.tokenManager.ParseAccessToken(accessToken)
	if err != nil {
		log.Error().Err(err).Msg("failed to parse access token")
		return uuid.Nil, false
	}

	userIDParsed, err := uuid.FromString(userID.(string))

	if err != nil {
		log.Error().Err(err).Msg("failed to parse user id")
		return uuid.Nil, false
	}

	exists, err := s.repository.DoesUserExistByID(ctx, userIDParsed)
	if err != nil {
		log.Error().Err(err).Msg("failed to check if user exists")
		return uuid.Nil, false
	}

	if !exists {
		return uuid.Nil, false
	}

	// set last seen time
	if err = s.repository.SetLastSeen(ctx, userIDParsed); err != nil {
		log.Error().Err(err).Msg("failed to set last seen")
		return uuid.Nil, false
	}

	return userIDParsed, true
}

func (s *TokenService) RefreshTokens(refreshToken string) (*model.Tokens, error) {
	subject, err := s.tokenManager.ParseRefreshToken(refreshToken)
	if err != nil {
		return nil, err
	}
	return s.GenerateTokens(subject.(string))
}

func (s *TokenService) GenerateTokens(subject string) (*model.Tokens, error) {

	tokens := &model.Tokens{}
	var err error
	tokens.AccessToken, err = s.tokenManager.NewAccessToken(subject)
	if err != nil {
		return nil, err
	}
	tokens.RefreshToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, err
	}

	tokens.PermissionsToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, err
	}

	return tokens, nil
}
