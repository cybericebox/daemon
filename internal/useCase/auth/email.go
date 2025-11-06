package auth

import (
	"context"
	"encoding/base64"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	IEmailService interface {
		GetUserByID(ctx context.Context, userID uuid.UUID) (*userModel.User, error)
		UpdateUserEmail(ctx context.Context, user userModel.User) error
		UpdateUserGoogleID(ctx context.Context, user userModel.User) error

		GetTemporalEmailConfirmationCodeData(
			ctx context.Context,
			code string,
		) (*temporalCodeModel.TemporalEmailConfirmationCodeData, error)
	}
)

func (u *AuthUseCase) ConfirmEmail(ctx context.Context, bsCode string) error {
	// Decode base64 temporal code
	code, err := base64.StdEncoding.DecodeString(bsCode)
	if err != nil {
		return temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(model.ErrPlatform.WithError(err).WithMessage("Failed to decode base64 code").Err()).Err()
	}

	// Get the temporal code from the database
	data, err := u.service.GetTemporalEmailConfirmationCodeData(ctx, string(code))
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get temporal email confirmation code data").Err()
	}

	user := userModel.User{
		ID:       data.UserID,
		Email:    data.Email,
		GoogleID: "",
	}

	// set user as current user in context
	ctx = tools.SetCurrentUserIDToContext(ctx, user.ID)

	// Update the user's email in the database
	if err = u.service.UpdateUserEmail(ctx, user); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update user email").Err()
	}

	if err = u.service.UpdateUserGoogleID(ctx, user); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update user google id").Err()
	}

	return nil
}
