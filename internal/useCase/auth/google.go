package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/model"
)

type (
	IGoogleService interface {
		CreateUser(ctx context.Context, newUser *model.User) (*model.User, error)
		UpdateUserPicture(ctx context.Context, user *model.User) error
		UpdateUserGoogleID(ctx context.Context, user *model.User) error

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
		return nil, err
	}

	user, err := u.service.GetUserByEmail(ctx, googleUser.Email)
	if err != nil && !errors.Is(err, model.ErrNotFound) {
		return nil, err
	}
	// if user exists
	if user != nil {
		// if user exists and googleID is not the same as in the database
		if user.GoogleID != googleUser.GoogleID {
			user.GoogleID = googleUser.GoogleID
			if err = u.service.UpdateUserGoogleID(ctx, user); err != nil {
				return nil, err
			}
		}

		// if picture is not set, update the user with the picture
		if user.Picture == "" {
			if err = u.service.UpdateUserPicture(ctx, user); err != nil {
				return nil, err
			}
		}

		// generate tokens and return them
		return u.service.GenerateTokens(user.ID.String())
	}

	// if user not exists

	// set default role to user
	googleUser.Role = model.UserRole

	// create user
	user, err = u.service.CreateUser(ctx, googleUser)
	if err != nil {
		return nil, err
	}

	// generate tokens and return them
	return u.service.GenerateTokens(user.ID.String())
}
