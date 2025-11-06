package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	emailModel "github.com/cybericebox/daemon/internal/model/email"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type ISelfService interface {
	GetUserByID(ctx context.Context, userID uuid.UUID) (*userModel.User, error)

	UpdateUserName(ctx context.Context, user userModel.User) error

	CreateTemporalEmailConfirmationCode(ctx context.Context, data temporalCodeModel.TemporalEmailConfirmationCodeData) (
		string,
		error,
	)

	SendEmailConfirmationEmail(ctx context.Context, sendTo string, data emailModel.EmailConfirmationTemplateData) error
}

func (u *AuthUseCase) GetSelfProfile(ctx context.Context) (*userModel.UserInfo, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user by id").Err()
	}

	return &userModel.UserInfo{
		ID:            user.ID,
		ConnectGoogle: user.GoogleID != "",
		Email:         user.Email,
		Name:          user.Name,
		Picture:       user.Picture,
		Role:          user.Role,
		LastSeen:      user.LastSeen,
	}, nil
}

func (u *AuthUseCase) UpdateSelfProfile(ctx context.Context, newUser userModel.User) error {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// get user by id
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user by id").Err()
	}

	user.Name = newUser.Name

	if err = u.service.UpdateUserName(ctx, *user); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update user name").Err()
	}

	// if email has been changed
	if user.Email != newUser.Email {
		// create temporal email confirmation code
		temporalCode, err := u.service.CreateTemporalEmailConfirmationCode(
			ctx, temporalCodeModel.TemporalEmailConfirmationCodeData{
				UserID: userID,
				Email:  newUser.Email,
			},
		)

		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create temporal email confirmation code").Err()
		}

		// normalize temporal code to base64
		bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", "")

		// send email confirmation email
		if err = u.service.SendEmailConfirmationEmail(
			ctx, newUser.Email, emailModel.EmailConfirmationTemplateData{
				Username: user.Name,
				Link: fmt.Sprintf(
					"%s://%s%s%s",
					config.SchemeHTTPS,
					config.PlatformDomain,
					emailModel.EmailConfirmationLink,
					bsCode,
				),
			},
		); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to send email confirmation email").Err()
		}
	}

	return nil
}

func (u *AuthUseCase) UpdateName(ctx context.Context, name string) error {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// get user by id
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user by id").Err()
	}

	user.Name = name

	if err = u.service.UpdateUserName(ctx, *user); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update user name").Err()
	}

	return nil
}
