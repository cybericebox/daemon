package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/model"
)

type (
	IGoogleService interface {
		CreateUser(ctx context.Context, newUser model.User) (*model.User, error)
		UpdateUserPicture(ctx context.Context, user model.User) error
		UpdateUserGoogleID(ctx context.Context, user model.User) error

		GetGoogleLoginURL() string
		GetGoogleUser(ctx context.Context, code, state string) (*model.User, error)
	}
)

func (u *AuthUseCase) GetGoogleLoginURL() string {
	return u.service.GetGoogleLoginURL()
}

func (u *AuthUseCase) GoogleAuth(ctx context.Context, code, state string) (*model.Tokens, error) {
	googleUser, err := u.service.GetGoogleUser(ctx, code, state)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to get google user").Cause()
	}

	user, err := u.service.GetUserByEmail(ctx, googleUser.Email)
	if err != nil && !errors.Is(err, model.ErrUserUserNotFound.Err()) {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to get user by email").Cause()
	}
	// if user does not exist
	if errors.Is(err, model.ErrUserUserNotFound.Err()) {
		// set default role to user
		googleUser.Role = model.UserRole
		// create user
		user, err = u.service.CreateUser(ctx, *googleUser)
		if err != nil {
			return nil, model.ErrAuth.WithError(err).WithMessage("Failed to create user").Cause()
		}

	} else {
		if user.GoogleID != googleUser.GoogleID {
			user.GoogleID = googleUser.GoogleID
			if err = u.service.UpdateUserGoogleID(ctx, *user); err != nil {
				return nil, model.ErrAuth.WithError(err).WithMessage("Failed to update user google id").Cause()
			}
		}

		// if picture is not set, update the user with the picture
		if user.Picture == "" {
			user.Picture = googleUser.Picture
			if err = u.service.UpdateUserPicture(ctx, *user); err != nil {
				return nil, model.ErrAuth.WithError(err).WithMessage("Failed to update user picture").Cause()
			}
		}
	}

	// generate tokens and return them
	tokens, err := u.service.GenerateTokens(user.ID)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to generate tokens").Cause()
	}

	return tokens, nil
}
