package auth

import (
	"context"
	"encoding/base64"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"strings"
)

type (
	IPasswordService interface {
		UpdateUserPassword(ctx context.Context, user model.User) error

		CreateTemporalPasswordResettingCode(ctx context.Context, data model.TemporalPasswordResettingCodeData) (string, error)
		GetTemporalPasswordResettingCodeData(ctx context.Context, code string) (*model.TemporalPasswordResettingCodeData, error)

		SendPasswordResettingEmail(ctx context.Context, sendTo string, data model.PasswordResettingTemplateData) error

		CheckPasswordComplexity(password string) error
		Hash(plaintextPassword string) (string, error)
		Matches(plaintextPassword, hashedPassword string) (bool, error)
	}
)

func (u *AuthUseCase) ForgotPassword(ctx context.Context, email string) error {
	userByEmail, err := u.service.GetUserByEmail(ctx, email)
	if err != nil {
		return err
	}

	// create a temporal code for the password resetting
	temporalCode, err := u.service.CreateTemporalPasswordResettingCode(ctx, model.TemporalPasswordResettingCodeData{
		UserID: userByEmail.ID,
	})
	if err != nil {
		return err
	}

	// normalize the temporal code to base64
	bsCode := strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", "")

	// send a password resetting email
	if err = u.service.SendPasswordResettingEmail(ctx, email, model.PasswordResettingTemplateData{
		Username: userByEmail.Name,
		Link:     fmt.Sprintf("%s://%s%s%s", config.SchemeHTTPS, config.PlatformDomain, model.PasswordResettingLink, bsCode),
	}); err != nil {
		return err
	}
	return nil
}

func (u *AuthUseCase) ResetPassword(ctx context.Context, bsCode, newPassword string) error {
	code, err := base64.StdEncoding.DecodeString(bsCode)
	if err != nil {
		return model.ErrInvalidTemporalCode
	}

	// get the temporal code data
	temporalCodeData, err := u.service.GetTemporalPasswordResettingCodeData(ctx, string(code))
	if err != nil {
		return err
	}

	// check the password complexity
	if err = u.service.CheckPasswordComplexity(newPassword); err != nil {
		return err
	}

	// hash the new password
	hashedPassword, err := u.service.Hash(newPassword)
	if err != nil {
		return err
	}

	// update the user password
	user := model.User{
		ID:             temporalCodeData.UserID,
		HashedPassword: hashedPassword,
	}

	if err = u.service.UpdateUserPassword(ctx, user); err != nil {
		return err
	}
	return nil
}

func (u *AuthUseCase) ChangePassword(ctx context.Context, oldPassword, newPassword string) error {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return err
	}

	// get user by id
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return err
	}

	// check if user is google user and has no password
	if user.HashedPassword == "" {
		return model.ErrInvalidOldPassword
	}

	// check old password
	matches, err := u.service.Matches(oldPassword, user.HashedPassword)
	if err != nil {
		return err
	}
	if !matches {
		return model.ErrInvalidOldPassword
	}

	// check new password complexity
	if err = u.service.CheckPasswordComplexity(newPassword); err != nil {
		return err
	}

	// hash new password
	hashedPassword, err := u.service.Hash(newPassword)
	if err != nil {
		return err
	}

	user.HashedPassword = hashedPassword

	// update user password
	if err = u.service.UpdateUserPassword(ctx, *user); err != nil {
		return err
	}
	return nil
}
