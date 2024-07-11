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

type (
	IEmailService interface {
		GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error)
		UpdateUserEmail(ctx context.Context, user model.User) error

		CreateTemporalEmailConfirmationCode(ctx context.Context, data model.TemporalEmailConfirmationCodeData) (string, error)
		GetTemporalEmailConfirmationCodeData(ctx context.Context, code string) (*model.TemporalEmailConfirmationCodeData, error)

		SendEmailConfirmationEmail(ctx context.Context, sendTo string, data model.EmailConfirmationTemplateData) error
	}
)

func (u *AuthUseCase) ChangeEmail(ctx context.Context, email string) error {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return err
	}

	// get user by id
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	// create temporal email confirmation code
	temporalCode, err := u.service.CreateTemporalEmailConfirmationCode(ctx, model.TemporalEmailConfirmationCodeData{
		UserID: userID,
		Email:  email,
	})

	if err != nil {
		return err
	}

	// normalize temporal code to base64
	bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", "")

	// send email confirmation email
	if err = u.service.SendEmailConfirmationEmail(ctx, email, model.EmailConfirmationTemplateData{
		Username: user.Name,
		Link:     fmt.Sprintf("%s://%s%s%s", config.SchemeHTTPS, config.PlatformDomain, model.EmailConfirmationLink, bsCode),
	}); err != nil {
		return err
	}
	return nil
}

func (u *AuthUseCase) ConfirmEmail(ctx context.Context, bsCode string) error {
	// Decode base64 temporal code
	code, err := base64.StdEncoding.DecodeString(bsCode)
	if err != nil {
		return model.ErrInvalidTemporalCode
	}

	// Get the temporal code from the database
	data, err := u.service.GetTemporalEmailConfirmationCodeData(ctx, string(code))
	if err != nil {
		return err
	}

	user := model.User{
		ID:    data.UserID,
		Email: data.Email,
	}

	// Update the user's email in the database
	if err = u.service.UpdateUserEmail(ctx, user); err != nil {
		return err
	}

	return nil
}
