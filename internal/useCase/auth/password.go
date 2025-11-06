package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	emailModel "github.com/cybericebox/daemon/internal/model/email"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	IPasswordService interface {
		UpdateUserPassword(ctx context.Context, user userModel.User) error

		CreateTemporalPasswordResettingCode(
			ctx context.Context,
			data temporalCodeModel.TemporalPasswordResettingCodeData,
		) (
			string,
			error,
		)
		GetTemporalPasswordResettingCodeData(
			ctx context.Context,
			code string,
		) (*temporalCodeModel.TemporalPasswordResettingCodeData, error)

		SendPasswordResettingEmail(
			ctx context.Context,
			sendTo string,
			data emailModel.PasswordResettingTemplateData,
		) error

		CheckPasswordComplexity(password string) error
		Hash(plaintextPassword string) (string, error)
		Matches(plaintextPassword, hashedPassword string) (bool, error)
	}
)

func (u *AuthUseCase) ForgotPassword(ctx context.Context, email string) error {
	userByEmail, err := u.service.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, userModel.ErrUserNotFound.Err()) {
			return nil
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user by email").Err()
	}

	// create a temporal code for the password resetting
	temporalCode, err := u.service.CreateTemporalPasswordResettingCode(
		ctx, temporalCodeModel.TemporalPasswordResettingCodeData{
			UserID: userByEmail.ID,
		},
	)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create temporal password resetting code").Err()
	}

	// normalize the temporal code to base64
	bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", "")

	// send a password resetting email
	if err = u.service.SendPasswordResettingEmail(
		ctx, email, emailModel.PasswordResettingTemplateData{
			Username: userByEmail.Name,
			Link: fmt.Sprintf(
				"%s://%s%s%s",
				config.SchemeHTTPS,
				config.PlatformDomain,
				emailModel.PasswordResettingLink,
				bsCode,
			),
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to send password resetting email").Err()
	}
	return nil
}

func (u *AuthUseCase) ResetPassword(ctx context.Context, bsCode, newPassword string) error {
	code, err := base64.StdEncoding.DecodeString(bsCode)
	if err != nil {
		return temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(model.ErrPlatform.WithError(err).WithMessage("Failed to decode base64 code").Err()).Err()
	}

	// get the temporal code data
	temporalCodeData, err := u.service.GetTemporalPasswordResettingCodeData(ctx, string(code))
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get temporal password resetting code data").Err()
	}

	// check the password complexity
	if err = u.service.CheckPasswordComplexity(newPassword); err != nil {
		return authModel.ErrAuthInvalidPasswordComplexity.WithError(err).Err()
	}

	// hash the new password
	hashedPassword, err := u.service.Hash(newPassword)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to hash the new password").Err()
	}

	// update the user password
	user := userModel.User{
		ID:             temporalCodeData.UserID,
		HashedPassword: hashedPassword,
	}

	// set user as current user in context
	ctx = tools.SetCurrentUserIDToContext(ctx, user.ID)

	if err = u.service.UpdateUserPassword(ctx, user); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update user password").Err()
	}
	return nil
}

func (u *AuthUseCase) UpdatePassword(ctx context.Context, oldPassword, newPassword string) error {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// get user by id
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user by id").Err()
	}

	// check if user is google user and has no password
	if user.HashedPassword == "" {
		return authModel.ErrAuthInvalidOldPassword.Err()
	}

	// check old password
	matches, err := u.service.Matches(oldPassword, user.HashedPassword)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check if old password matches").Err()
	}
	if !matches {
		return authModel.ErrAuthInvalidOldPassword.Err()
	}

	// check new password complexity
	if err = u.service.CheckPasswordComplexity(newPassword); err != nil {
		return authModel.ErrAuthInvalidPasswordComplexity.WithError(err).Err()
	}

	// hash new password
	hashedPassword, err := u.service.Hash(newPassword)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to hash the new password").Err()
	}

	user.HashedPassword = hashedPassword

	// update user password
	if err = u.service.UpdateUserPassword(ctx, *user); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update user password").Err()
	}
	return nil
}
