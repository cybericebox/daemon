package user

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	emailModel "github.com/cybericebox/daemon/internal/model/email"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	UserUseCase struct {
		service IUserService
	}

	IUserService interface {
		GetUsers(ctx context.Context, search string, page, pageSize int) ([]*userModel.UserInfo, error)
		GetUserByID(ctx context.Context, id uuid.UUID) (*userModel.User, error)
		GetUserByEmail(ctx context.Context, email string) (*userModel.User, error)

		UpdateUserRole(ctx context.Context, user userModel.User) error

		DeleteUser(ctx context.Context, id uuid.UUID) error

		CreateTemporalContinueRegistrationCode(
			ctx context.Context,
			data temporalCodeModel.TemporalContinueRegistrationCodeData,
		) (string, error)

		SendInvitationToRegistrationEmail(
			ctx context.Context,
			sendTo string,
			data emailModel.InvitationToRegistrationTemplateData,
		) error
	}

	Dependencies struct {
		Service IUserService
	}
)

func NewUseCase(deps Dependencies) *UserUseCase {
	return &UserUseCase{
		service: deps.Service,
	}

}

func (u *UserUseCase) GetUsers(ctx context.Context, search string, page, pageSize int) ([]*userModel.UserInfo, error) {
	users, err := u.service.GetUsers(ctx, search, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get users").Err()
	}
	return users, nil
}

func (u *UserUseCase) GetCurrentUserRole(ctx context.Context) (string, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get user by id").Err()
	}
	return user.Role, nil
}

func (u *UserUseCase) InviteUsers(ctx context.Context, data userModel.InviteUsers) error {
	var errs error
	for _, email := range data.Emails {
		// Check if the user with the email already exists
		user, err := u.service.GetUserByEmail(ctx, email)
		if err != nil && !errors.Is(err, userModel.ErrUserNotFound.Err()) {
			errs = multierror.Append(
				errs,
				model.ErrPlatform.WithError(err).WithMessage("Failed to get user by email").Err(),
			)
			continue
		}

		// If the user exists, send an email with the information that the account already exists
		if user != nil {
			continue
		}

		// Create a temporal code for the registration
		temporalCode, err := u.service.CreateTemporalContinueRegistrationCode(
			ctx, temporalCodeModel.TemporalContinueRegistrationCodeData{
				Email: email,
				Role:  data.Role,
			},
		)
		if err != nil {
			errs = multierror.Append(
				errs,
				model.ErrPlatform.WithError(err).WithMessage("Failed to create temporal continue registration code").Err(),
			)
			continue
		}

		// Normalize the temporal code to base64 and create a token with the email and the temporal code
		bsToken := fmt.Sprintf(
			"%s!%s",
			strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", ""),
			strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(email)), "=", ""),
		)

		// Send a registration email
		if err = u.service.SendInvitationToRegistrationEmail(
			ctx, email, emailModel.InvitationToRegistrationTemplateData{
				Link: fmt.Sprintf(
					"%s://%s%s%s",
					config.SchemeHTTPS,
					config.PlatformDomain,
					emailModel.ContinueRegistrationLink,
					bsToken,
				),
			},
		); err != nil {
			errs = multierror.Append(
				errs,
				model.ErrPlatform.WithError(err).WithMessage("Failed to send continue registration email").Err(),
			)
			continue
		}
	}

	if errs != nil {
		return errs
	}

	return nil
}

func (u *UserUseCase) UpdateUserRole(ctx context.Context, user userModel.User) error {
	if err := u.service.UpdateUserRole(ctx, user); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update user role").Err()
	}
	return nil
}

func (u *UserUseCase) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	if err := u.service.DeleteUser(ctx, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete user").Err()
	}
	return nil
}
