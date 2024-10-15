package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"strings"
)

type (
	ISignUpService interface {
		CreateUser(ctx context.Context, newUser model.User) (*model.User, error)

		CreateTemporalContinueRegistrationCode(ctx context.Context, data model.TemporalContinueRegistrationCodeData) (string, error)
		GetTemporalContinueRegistrationCodeData(ctx context.Context, code string) (*model.TemporalContinueRegistrationCodeData, error)

		SendContinueRegistrationEmail(ctx context.Context, sendTo string, data model.ContinueRegistrationTemplateData) error
		SendAccountExistsEmail(ctx context.Context, sendTo string, data model.AccountExistsTemplateData) error

		CheckPasswordComplexity(password string) error
		Hash(plaintextPassword string) (string, error)
	}
)

func (u *AuthUseCase) SignUp(ctx context.Context, email string) error {
	// Check if the user with the email already exists
	user, err := u.service.GetUserByEmail(ctx, email)
	if err != nil && !errors.Is(err, model.ErrUserUserNotFound.Err()) {
		return model.ErrAuth.WithError(err).WithMessage("Failed to get user by email").Cause()
	}

	// If the user exists, send an email with the information that the account already exists
	if user != nil {
		if err = u.service.SendAccountExistsEmail(ctx, email, model.AccountExistsTemplateData{
			Username: user.Name,
		}); err != nil {
			return model.ErrAuth.WithError(err).WithMessage("Failed to send account exists email").Cause()
		}
	}

	// Create a temporal code for the registration
	temporalCode, err := u.service.CreateTemporalContinueRegistrationCode(ctx, model.TemporalContinueRegistrationCodeData{
		Email: email,
		Role:  model.UserRole,
	})
	if err != nil {
		return model.ErrAuth.WithError(err).WithMessage("Failed to create temporal continue registration code").Cause()
	}

	// Normalize the temporal code to base64 and create a token with the email and the temporal code
	bsToken := fmt.Sprintf("%s!%s",
		strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", ""),
		strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(email)), "=", ""),
	)

	// Send a registration email
	if err = u.service.SendContinueRegistrationEmail(ctx, email, model.ContinueRegistrationTemplateData{
		Link: fmt.Sprintf("%s://%s%s%s", config.SchemeHTTPS, config.PlatformDomain, model.ContinueRegistrationLink, bsToken),
	}); err != nil {
		return model.ErrAuth.WithError(err).WithMessage("Failed to send continue registration email").Cause()
	}

	return nil
}

func (u *AuthUseCase) SignUpContinue(ctx context.Context, bsCode string, newUser model.User) (*model.Tokens, error) {
	// Decode base64 temporal code
	code, err := base64.StdEncoding.DecodeString(bsCode)
	if err != nil {
		return nil, model.ErrTemporalCodeInvalidCode.WithError(model.ErrAuth.WithError(err).WithMessage("Failed to decode base64 code").Cause()).Cause()
	}

	// Get the temporal code from the database
	data, err := u.service.GetTemporalContinueRegistrationCodeData(ctx, string(code))
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to get temporal continue registration code data").Cause()
	}

	// Check password complexity
	if err = u.service.CheckPasswordComplexity(newUser.Password); err != nil {
		return nil, model.ErrAuthInvalidPasswordComplexity.WithError(err).Cause()
	}

	// Hash the password
	hashedPassword, err := u.service.Hash(newUser.Password)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to hash the password").Cause()
	}

	newUser.Role = data.Role
	newUser.Email = data.Email
	newUser.HashedPassword = hashedPassword

	user, err := u.service.CreateUser(ctx, newUser)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to create user").Cause()
	}

	tokes, err := u.service.GenerateTokens(user.ID)
	if err != nil {
		return nil, model.ErrAuth.WithError(err).WithMessage("Failed to generate tokens").Cause()
	}

	return tokes, nil
}
