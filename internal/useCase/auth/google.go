package auth

import (
	"context"
	"errors"

	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	IGoogleService interface {
		CreateUser(ctx context.Context, newUser userModel.User) (*userModel.User, error)
		UpdateUserPicture(ctx context.Context, user userModel.User) error
		UpdateUserGoogleID(ctx context.Context, user userModel.User) error

		GetGoogleLoginURL() (string, error)
		GetGoogleUser(ctx context.Context, code, state string) (*userModel.User, error)
	}
)

func (u *AuthUseCase) GetGoogleLoginURL() (string, error) {
	return u.service.GetGoogleLoginURL()
}

func (u *AuthUseCase) GoogleAuth(ctx context.Context, code, state string) (*authModel.Tokens, error) {
	googleUser, err := u.service.GetGoogleUser(ctx, code, state)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get google user").Err()
	}

	user, err := u.service.GetUserByEmail(ctx, googleUser.Email)
	if err != nil && !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user by email").Err()
	}
	// if user does not exist
	if errors.Is(err, userModel.ErrUserNotFound.Err()) {
		// set default role to user
		googleUser.Role = userModel.UserRole
		// create user
		user, err = u.service.CreateUser(ctx, *googleUser)
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to create user").Err()
		}

	} else {
		// set user as current user in context
		ctx = tools.SetCurrentUserIDToContext(ctx, user.ID)

		if user.GoogleID != googleUser.GoogleID {
			user.GoogleID = googleUser.GoogleID
			if err = u.service.UpdateUserGoogleID(ctx, *user); err != nil {
				return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to update user google id").Err()
			}
		}

		// if picture is not set, update the user with the picture
		if user.Picture == "" {
			user.Picture = googleUser.Picture
			if err = u.service.UpdateUserPicture(ctx, *user); err != nil {
				return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to update user picture").Err()
			}
		}
	}

	// generate tokens and return them
	tokens, err := u.service.GenerateTokens(user.ID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to generate tokens").Err()
	}

	return tokens, nil
}
