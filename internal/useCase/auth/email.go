package auth

import (
	"context"
	"encoding/base64"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	IEmailService interface {
		GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error)
		UpdateUserEmail(ctx context.Context, user model.User) error
		UpdateUserGoogleID(ctx context.Context, user model.User) error

		GetTemporalEmailConfirmationCodeData(ctx context.Context, code string) (*model.TemporalEmailConfirmationCodeData, error)
	}
)

func (u *AuthUseCase) ConfirmEmail(ctx context.Context, bsCode string) error {
	// Decode base64 temporal code
	code, err := base64.StdEncoding.DecodeString(bsCode)
	if err != nil {
		return model.ErrTemporalCodeInvalidCode.WithError(model.ErrAuth.WithError(err).WithMessage("Failed to decode base64 code").Cause()).Cause()
	}

	// Get the temporal code from the database
	data, err := u.service.GetTemporalEmailConfirmationCodeData(ctx, string(code))
	if err != nil {
		return model.ErrAuth.WithError(err).WithMessage("Failed to get temporal email confirmation code data").Cause()
	}

	user := model.User{
		ID:       data.UserID,
		Email:    data.Email,
		GoogleID: "",
	}

	// Update the user's email in the database
	if err = u.service.UpdateUserEmail(ctx, user); err != nil {
		return model.ErrAuth.WithError(err).WithMessage("Failed to update user email").Cause()
	}

	if err = u.service.UpdateUserGoogleID(ctx, user); err != nil {
		return model.ErrAuth.WithError(err).WithMessage("Failed to update user google id").Cause()
	}

	return nil
}
