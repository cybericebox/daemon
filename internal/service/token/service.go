package token

import (
	"errors"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/lib/pkg/libError"
	"github.com/cybericebox/lib/pkg/token"
	"github.com/rs/zerolog/log"
	"time"
)

const (
	issuer = "auth"
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
	manager, err := token.NewAccessRefreshTokenManager(token.AccessRefreshTokenDependencies{
		SigningKey: deps.Config.TokenSignature,
		Issuer:     issuer,
		AccessTTL:  deps.Config.AccessTokenTTL,
		RefreshTTL: deps.Config.RefreshTokenTTL,
	})
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
		if errors.Is(err, libError.ErrTokenInvalidJWTToken.Err()) {
			return nil, authModel.ErrAuthInvalidAccessToken.Err()
		}
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to parse access token").Err()
	}

	return subject, nil
}

func (s *TokenService) RefreshTokens(refreshToken string) (*authModel.Tokens, interface{}, error) {
	subject, err := s.tokenManager.ParseRefreshToken(refreshToken)
	if err != nil {
		if errors.Is(err, libError.ErrTokenInvalidJWTToken.Err()) {
			return nil, nil, authModel.ErrAuthInvalidRefreshToken.Err()
		}
		return nil, nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to parse refresh token").Err()
	}

	tokens, err := s.GenerateTokens(subject)
	if err != nil {
		return nil, nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to generate tokens").Err()
	}

	return tokens, subject, nil
}

func (s *TokenService) GenerateTokens(subject interface{}) (*authModel.Tokens, error) {
	tokens := authModel.Tokens{}
	var err error

	tokens.AccessToken, err = s.tokenManager.NewAccessToken(subject)
	if err != nil {
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to generate access token").Err()
	}

	tokens.RefreshToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to generate refresh token").Err()
	}

	tokens.PermissionsToken, err = s.tokenManager.NewRefreshToken(subject)
	if err != nil {
		return nil, authModel.ErrAuth.WithError(err).WithMessage("Failed to generate permissions token").Err()
	}

	return &tokens, nil
}
