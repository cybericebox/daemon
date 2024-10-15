package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"strings"
)

type ISelfService interface {
	GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error)

	UpdateUserName(ctx context.Context, user model.User) error

	CreateTemporalEmailConfirmationCode(ctx context.Context, data model.TemporalEmailConfirmationCodeData) (string, error)

	SendEmailConfirmationEmail(ctx context.Context, sendTo string, data model.EmailConfirmationTemplateData) error
}

func (u *AuthUseCase) GetSelfProfile(ctx context.Context) (*model.UserInfo, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return nil, model.ErrUser.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return nil, model.ErrUser.WithError(err).WithMessage("Failed to get user by id").Cause()
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

func (u *AuthUseCase) UpdateSelfProfile(ctx context.Context, newUser model.User) error {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// get user by id
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to get user by id").Cause()
	}

	user.Name = newUser.Name

	if err = u.service.UpdateUserName(ctx, *user); err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to update user name").Cause()
	}

	// if email has been changed
	if user.Email != newUser.Email {
		// create temporal email confirmation code
		temporalCode, err := u.service.CreateTemporalEmailConfirmationCode(ctx, model.TemporalEmailConfirmationCodeData{
			UserID: userID,
			Email:  newUser.Email,
		})

		if err != nil {
			return model.ErrUser.WithError(err).WithMessage("Failed to create temporal email confirmation code").Cause()
		}

		// normalize temporal code to base64
		bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", "")

		// send email confirmation email
		if err = u.service.SendEmailConfirmationEmail(ctx, newUser.Email, model.EmailConfirmationTemplateData{
			Username: user.Name,
			Link:     fmt.Sprintf("%s://%s%s%s", config.SchemeHTTPS, config.PlatformDomain, model.EmailConfirmationLink, bsCode),
		}); err != nil {
			return model.ErrUser.WithError(err).WithMessage("Failed to send email confirmation email").Cause()
		}
	}

	return nil
}

func (u *AuthUseCase) UpdateName(ctx context.Context, name string) error {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// get user by id
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to get user by id").Cause()
	}

	user.Name = name

	if err = u.service.UpdateUserName(ctx, *user); err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to update user name").Cause()
	}

	return nil
}
