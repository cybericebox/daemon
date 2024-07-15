package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/appError"
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
		return nil, appError.NewError().WithError(err).WithMessage("failed to get google user")
	}

	user, err := u.service.GetUserByEmail(ctx, googleUser.Email)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get user by email")
	}
	// if user does not exist
	if errors.Is(err, model.ErrNotFound) {
		// set default role to user
		googleUser.Role = model.UserRole
		// create user
		user, err = u.service.CreateUser(ctx, *googleUser)
		if err != nil {
			return nil, appError.NewError().WithError(err).WithMessage("failed to create user")
		}

	} else {
		if user.GoogleID != googleUser.GoogleID {
			user.GoogleID = googleUser.GoogleID
			if err = u.service.UpdateUserGoogleID(ctx, *user); err != nil {
				return nil, appError.NewError().WithError(err).WithMessage("failed to update user google id")
			}
		}

		// if picture is not set, update the user with the picture
		if user.Picture == "" {
			user.Picture = googleUser.Picture
			if err = u.service.UpdateUserPicture(ctx, *user); err != nil {
				return nil, appError.NewError().WithError(err).WithMessage("failed to update user picture")
			}
		}
	}

	// generate tokens and return them
	tokens, err := u.service.GenerateTokens(user.ID.String())
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to generate tokens")
	}

	return tokens, nil
}
