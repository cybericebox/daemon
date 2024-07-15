package user

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	UserUseCase struct {
		service IUserService
	}

	IUserService interface {
		GetUsers(ctx context.Context, search string) ([]*model.UserInfo, error)
		GetUserByID(ctx context.Context, id uuid.UUID) (*model.User, error)

		UpdateUserRole(ctx context.Context, user model.User) error

		DeleteUser(ctx context.Context, id uuid.UUID) error
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
		return nil, appError.NewError().WithError(err).WithMessage("failed to get users")
	}
	return users, nil
}

func (u *UserUseCase) GetCurrentUserRole(ctx context.Context) (string, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to get user id from context")
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to get user by id")
	}
	return user.Role, nil
}

func (u *UserUseCase) UpdateUserRole(ctx context.Context, user model.User) error {
	if err := u.service.UpdateUserRole(ctx, user); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to update user role")
	}
	return nil
}

func (u *UserUseCase) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	if err := u.service.DeleteUser(ctx, userID); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete user")
	}
	return nil
}
