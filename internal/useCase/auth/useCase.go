package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/appError"
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

		GetUserByEmail(ctx context.Context, email string) (*model.User, error)
		GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error)

		Matches(password, hashedPassword string) (bool, error)

		ValidateAccessToken(ctx context.Context, accessToken string) (uuid.UUID, error)
		RefreshTokens(refreshToken string) (*model.Tokens, uuid.UUID, error)
		GenerateTokens(subject string) (*model.Tokens, error)

		GetEventByTag(ctx context.Context, eventTag string) (*model.Event, error)
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
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get user by email")
	}
	// if user not found emulate password check and after return invalid user credentials error
	if errors.Is(err, model.ErrNotFound) || user.HashedPassword == "" {
		_, err = u.service.Matches(password, fakeHashedPassword)
		if err != nil {
			return nil, appError.NewError().WithError(err).WithMessage("failed to check password")
		}
		return nil, model.ErrInvalidUserCredentials
	}

	// if user has password, check it
	matches, err := u.service.Matches(password, user.HashedPassword)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to check password")
	}

	if !matches {
		return nil, model.ErrInvalidUserCredentials
	}

	// if password is correct generate tokens and return them
	tokens, err := u.service.GenerateTokens(user.ID.String())
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to generate tokens")
	}
	return tokens, nil
}

func (u *AuthUseCase) RefreshTokensAndReturnUserID(ctx context.Context, oldTokens model.Tokens) *model.CheckTokensResult {
	userID, err := u.service.ValidateAccessToken(ctx, oldTokens.AccessToken)
	if err == nil {
		return &model.CheckTokensResult{
			Tokens: &oldTokens,
			UserID: userID,
			Valid:  true,
		}
	} else {
		log.Debug().Err(err).Msg("Failed to validate access token")
	}
	// if access token is not valid, try to refresh tokens
	tokens, userID, err := u.service.RefreshTokens(oldTokens.RefreshToken)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to refresh tokens")
		return nil
	}

	return &model.CheckTokensResult{
		Tokens:    tokens,
		UserID:    userID,
		Refreshed: true,
		Valid:     true,
	}
}

func (u *AuthUseCase) GetSelfProfile(ctx context.Context) (*model.UserInfo, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get user id from context")
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get user by id")
	}

	return &model.UserInfo{
		ID:            user.ID,
		ConnectGoogle: user.GoogleID != "",
		Email:         user.Email,
		Name:          user.Name,
		Picture:       user.Picture,
		Role:          user.Role,
		LastSeen:      user.LastSeen,
	}, nil
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
		eventTag, err := tools.GetSubdomainFromContext(ctx)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to get event id from context")
			return true
		}

		event, err := u.service.GetEventByTag(ctx, eventTag)
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
		eventTag, err := tools.GetSubdomainFromContext(ctx)
		if err != nil {
			log.Debug().Err(err).Msg("Failed to get event id from context")
			return true
		}

		event, err := u.service.GetEventByTag(ctx, eventTag)
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
