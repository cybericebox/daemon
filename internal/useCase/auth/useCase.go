package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"strings"
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
		ISelfService

		GetUserByEmail(ctx context.Context, email string) (*model.User, error)

		Matches(password, hashedPassword string) (bool, error)

		ValidateAccessToken(accessToken string) (interface{}, error)
		RefreshTokens(refreshToken string) (*model.Tokens, interface{}, error)
		GenerateTokens(subject interface{}) (*model.Tokens, error)

		GetEventByTag(ctx context.Context, eventTag string) (*model.Event, error)

		SetLastSeen(ctx context.Context, id uuid.UUID) error
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
	if err != nil && !errors.Is(err, model.ErrUserUserNotFound.Err()) {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to get user by email").Cause()
	}
	// if user not found emulate password check and after return invalid user credentials error
	if errors.Is(err, model.ErrUserUserNotFound.Err()) || user.HashedPassword == "" {
		_, err = u.service.Matches(password, fakeHashedPassword)
		if err != nil {
			return nil, model.ErrAuth.WithError(err).WithMessage("Failed to check password").Cause()
		}
		return nil, model.ErrAuthInvalidUserCredentials.Cause()
	}

	// if user has password, check it
	matches, err := u.service.Matches(password, user.HashedPassword)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to check password").Cause()
	}

	if !matches {
		return nil, model.ErrAuthInvalidUserCredentials.Cause()
	}

	// if password is correct generate tokens and return them
	tokens, err := u.service.GenerateTokens(user.ID)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to generate tokens").Cause()
	}
	return tokens, nil
}

func (u *AuthUseCase) RefreshTokensAndReturnUserID(ctx context.Context, oldTokens model.Tokens) *model.CheckTokensResult {
	subject, err := u.service.ValidateAccessToken(oldTokens.AccessToken)
	if err == nil {
		userID, err := uuid.FromString(subject.(string))
		if err != nil {
			log.Debug().Err(err).Msg("Failed to convert subject to uuid")
			return nil
		}

		// set last seen
		if err = u.service.SetLastSeen(ctx, userID); err != nil {
			if !errors.Is(err, model.ErrUserUserNotFound.Err()) {
				log.Debug().Err(err).Msg("Failed to set last seen")
				return nil
			}
			return &model.CheckTokensResult{
				Tokens: &oldTokens,
				UserID: userID,
				Valid:  false,
			}
		}

		return &model.CheckTokensResult{
			Tokens: &oldTokens,
			UserID: userID,
			Valid:  true,
		}
	} else {
		log.Debug().Err(err).Msg("Failed to validate access token")
	}
	// if access token is not valid, try to refresh tokens
	tokens, subject, err := u.service.RefreshTokens(oldTokens.RefreshToken)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to refresh tokens")
		return nil
	}

	userID, err := uuid.FromString(subject.(string))
	if err != nil {
		log.Debug().Err(err).Msg("Failed to convert subject to uuid")
		return nil
	}

	// set last seen
	if err = u.service.SetLastSeen(ctx, userID); err != nil {
		log.Debug().Err(err).Msg("Failed to set last seen in refresh")
		if !errors.Is(err, model.ErrUserUserNotFound.Err()) {
			log.Debug().Err(err).Msg("Failed to set last seen")
			return nil
		}
		return &model.CheckTokensResult{
			Tokens: &oldTokens,
			UserID: userID,
			Valid:  false,
		}
	}

	return &model.CheckTokensResult{
		Tokens:    tokens,
		UserID:    userID,
		Refreshed: true,
		Valid:     true,
	}
}

func (u *AuthUseCase) URLNeedsProtection(ctx context.Context, url string) bool {
	// get subdomain from context
	subdomain, err := tools.GetSubdomainFromContext(ctx)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to get subdomain from context")
		return true
	}

	// if url is scoreboards check dynamically
	if strings.HasPrefix(url, "/scoreboard") {
		event, err := u.service.GetEventByTag(ctx, subdomain)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to get event by id")
			return true
		}

		// if event scoreboard is public, then return true
		if event.ScoreboardAvailability == model.PublicScoreboardAvailabilityType {
			return false
		}

		// protect by default
		return true
	}

	// if url is teams check dynamically
	if strings.HasPrefix(url, "/teams") {
		event, err := u.service.GetEventByTag(ctx, subdomain)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to get event by id")
			return true
		}

		// if event scoreboard is public, then return true
		if event.ParticipantsVisibility == model.PublicParticipantsVisibilityType {
			return false
		}

		// protect by default
		return true
	}

	return strings.HasPrefix(url, "/profile") || strings.HasPrefix(url, "/challenges") || subdomain == config.AdminSubdomain
}
