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
)

type (
	ISignUpService interface {
		CreateUser(ctx context.Context, newUser userModel.User) (*userModel.User, error)

		CreateTemporalContinueRegistrationCode(
			ctx context.Context,
			data temporalCodeModel.TemporalContinueRegistrationCodeData,
		) (string, error)
		GetTemporalContinueRegistrationCodeData(
			ctx context.Context,
			code string,
		) (*temporalCodeModel.TemporalContinueRegistrationCodeData, error)

		SendContinueRegistrationEmail(
			ctx context.Context,
			sendTo string,
			data emailModel.ContinueRegistrationTemplateData,
		) error
		SendAccountExistsEmail(ctx context.Context, sendTo string, data emailModel.AccountExistsTemplateData) error

		CheckPasswordComplexity(password string) error
		Hash(plaintextPassword string) (string, error)
	}
)

func (u *AuthUseCase) SignUp(ctx context.Context, email string) error {
	// Check if the user with the email already exists
	user, err := u.service.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, userModel.ErrUserNotFound.Err()) {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user by email").Err()
	}

	// If the user exists, send an email with the information that the account already exists
	if user != nil {
		if err = u.service.SendAccountExistsEmail(
			ctx, email, emailModel.AccountExistsTemplateData{
				Username: user.Name,
			},
		); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to send account exists email").Err()
		}
	}

	// Create a temporal code for the registration
	temporalCode, err := u.service.CreateTemporalContinueRegistrationCode(
		ctx, temporalCodeModel.TemporalContinueRegistrationCodeData{
			Email: email,
			Role:  userModel.UserRole,
		},
	)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create temporal continue registration code").Err()
	}

	// Normalize the temporal code to base64 and create a token with the email and the temporal code
	bsToken := fmt.Sprintf(
		"%s!%s",
		strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", ""),
		strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(email)), "=", ""),
	)

	// Send a registration email
	if err = u.service.SendContinueRegistrationEmail(
		ctx, email, emailModel.ContinueRegistrationTemplateData{
			Link: fmt.Sprintf(
				"%s://%s%s%s",
				config.SchemeHTTPS,
				config.PlatformDomain,
				emailModel.ContinueRegistrationLink,
				bsToken,
			),
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to send continue registration email").Err()
	}

	return nil
}

func (u *AuthUseCase) SignUpContinue(ctx context.Context, bsCode string, newUser userModel.User) (
	*authModel.Tokens,
	error,
) {
	// Decode base64 temporal code
	code, err := base64.StdEncoding.DecodeString(bsCode)
	if err != nil {
		return nil, temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(model.ErrPlatform.WithError(err).WithMessage("Failed to decode base64 code").Err()).Err()
	}

	// Get the temporal code from the database
	data, err := u.service.GetTemporalContinueRegistrationCodeData(ctx, string(code))
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get temporal continue registration code data").Err()
	}

	// Check password complexity
	if err = u.service.CheckPasswordComplexity(newUser.Password); err != nil {
		return nil, authModel.ErrAuthInvalidPasswordComplexity.WithError(err).Err()
	}

	// Hash the password
	hashedPassword, err := u.service.Hash(newUser.Password)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to hash the password").Err()
	}

	newUser.Role = data.Role
	newUser.Email = data.Email
	newUser.HashedPassword = hashedPassword

	user, err := u.service.CreateUser(ctx, newUser)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to create user").Err()
	}

	tokes, err := u.service.GenerateTokens(user.ID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to generate tokens").Err()
	}

	return tokes, nil
}
