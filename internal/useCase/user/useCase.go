package user

import (
	"context"
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

		UpdateUserRole(ctx context.Context, user *model.User) error

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
	return u.service.GetUsers(ctx, search)
}

func (u *UserUseCase) GetCurrentUserRole(ctx context.Context) (string, error) {
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return "", err
	}

	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return "", err
	}
	return user.Role, nil
}

func (u *UserUseCase) UpdateUserRole(ctx context.Context, userID uuid.UUID, role string) error {
	user := &model.User{
		ID:   userID,
		Role: role,
	}

	return u.service.UpdateUserRole(ctx, user)
}

func (u *UserUseCase) DeleteUser(ctx context.Context, userID uuid.UUID) error {
	return u.service.DeleteUser(ctx, userID)
}
