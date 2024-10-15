package user

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"strings"
)

type (
	UserUseCase struct {
		service IUserService
	}

	IUserService interface {
		GetUsers(ctx context.Context, search string) ([]*model.UserInfo, error)
		GetUserByID(ctx context.Context, id uuid.UUID) (*model.User, error)
		GetUserByEmail(ctx context.Context, email string) (*model.User, error)

		UpdateUserRole(ctx context.Context, user model.User) error

		DeleteUser(ctx context.Context, id uuid.UUID) error

		CreateTemporalContinueRegistrationCode(ctx context.Context, data model.TemporalContinueRegistrationCodeData) (string, error)

		SendInvitationToRegistrationEmail(ctx context.Context, sendTo string, data model.InvitationToRegistrationTemplateData) error
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

func (u *UserUseCase) GetUsers(ctx context.Context, search string) ([]*model.UserInfo, error) {
	users, err := u.service.GetUsers(ctx, search)
	if err != nil {
		return nil, model.ErrUser.WithError(err).WithMessage("Failed to get users").Cause()
	}
	return users, nil
}

func (u *UserUseCase) GetCurrentUserRole(ctx context.Context) (string, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return "", model.ErrUser.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return "", model.ErrUser.WithError(err).WithMessage("Failed to get user by id").Cause()
	}
	return user.Role, nil
}

func (u *UserUseCase) InviteUsers(ctx context.Context, data model.InviteUsers) error {
	var errs error
	for _, email := range data.Emails {
		// Check if the user with the email already exists
		user, err := u.service.GetUserByEmail(ctx, email)
		if err != nil && !errors.Is(err, model.ErrUserUserNotFound.Err()) {
			errs = multierror.Append(errs, model.ErrUser.WithError(err).WithMessage("Failed to get user by email").Cause())
			continue
		}

		// If the user exists, send an email with the information that the account already exists
		if user != nil {
			continue
		}

		// Create a temporal code for the registration
		temporalCode, err := u.service.CreateTemporalContinueRegistrationCode(ctx, model.TemporalContinueRegistrationCodeData{
			Email: email,
			Role:  data.Role,
		})
		if err != nil {
			errs = multierror.Append(errs, model.ErrUser.WithError(err).WithMessage("Failed to create temporal continue registration code").Cause())
			continue
		}

		// Normalize the temporal code to base64 and create a token with the email and the temporal code
		bsToken := fmt.Sprintf("%s!%s",
			strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(temporalCode)), "=", ""),
			strings.ReplaceAll(base64.StdEncoding.EncodeToString([]byte(email)), "=", ""),
		)

		// Send a registration email
		if err = u.service.SendInvitationToRegistrationEmail(ctx, email, model.InvitationToRegistrationTemplateData{
			Link: fmt.Sprintf("%s://%s%s%s", config.SchemeHTTPS, config.PlatformDomain, model.ContinueRegistrationLink, bsToken),
		}); err != nil {
			errs = multierror.Append(errs, model.ErrUser.WithError(err).WithMessage("Failed to send continue registration email").Cause())
			continue
		}
	}

	if errs != nil {
		return errs
	}

	return nil
}

func (u *UserUseCase) UpdateUserRole(ctx context.Context, user model.User) error {
	if err := u.service.UpdateUserRole(ctx, user); err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to update user role").Cause()
	}
	return nil
}

func (u *UserUseCase) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	if err := u.service.DeleteUser(ctx, userID); err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to delete user").Cause()
	}
	return nil
}
