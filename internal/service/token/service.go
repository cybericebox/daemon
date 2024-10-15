package token

import (
	"errors"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/token"
	"github.com/rs/zerolog/log"
	"time"
)

type (
	TokenService struct {
		config       *config.JWTConfig
		tokenManager tokenManager
	}

	tokenManager interface {
		NewAccessToken(subject interface{}, ttl ...time.Duration) (string, error)
		NewRefreshToken(subject interface{}, ttl ...time.Duration) (string, error)
		ParseAccessToken(token string) (interface{}, error)
		ParseRefreshToken(token string) (interface{}, error)
	}

	Dependencies struct {
		Config *config.JWTConfig
	}
)

func NewTokenService(deps Dependencies) *TokenService {
	manager, err := token.NewTokenManager(deps.Config.TokenSignature, deps.Config.AccessTokenTTL, deps.Config.RefreshTokenTTL)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create token manager")
	}
	return &TokenService{
		config:       deps.Config,
		tokenManager: manager,
	}
}

func (s *TokenService) ValidateAccessToken(accessToken string) (interface{}, error) {
	subject, err := s.tokenManager.ParseAccessToken(accessToken)
	if err != nil {
		if errors.Is(err, token.InvalidJWTToken) {
			return nil, model.ErrAuthInvalidAccessToken.Cause()
		}
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to parse access token").Cause()
	}

	return subject, nil
}

func (s *TokenService) RefreshTokens(refreshToken string) (*model.Tokens, interface{}, error) {
	subject, err := s.tokenManager.ParseRefreshToken(refreshToken)
	if err != nil {
		if errors.Is(err, token.InvalidJWTToken) {
			return nil, nil, model.ErrAuthInvalidRefreshToken.Cause()
		}
		return nil, nil, model.ErrAuth.WithError(err).WithMessage("Failed to parse refresh token").Cause()
	}

	tokens, err := s.GenerateTokens(subject)
	if err != nil {
		return nil, nil, model.ErrAuth.WithError(err).WithMessage("Failed to generate tokens").Cause()
	}

	return tokens, subject, nil
}

func (s *TokenService) GenerateTokens(subject interface{}) (*model.Tokens, error) {
	tokens := model.Tokens{}
	var err error

	tokens.AccessToken, err = s.tokenManager.NewAccessToken(subject)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to generate access token").Cause()
	}

	tokens.RefreshToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to generate refresh token").Cause()
	}

	tokens.PermissionsToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to generate permissions token").Cause()
	}

	return &tokens, nil
}
