package user

import (
	"context"
	"database/sql"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	UserService struct {
		repository IRepository
	}

	IRepository interface {
		CreateUser(ctx context.Context, arg postgres.CreateUserParams) error

		DoesUserExistByID(ctx context.Context, id uuid.UUID) (bool, error)

		GetAllUsers(ctx context.Context) ([]postgres.GetAllUsersRow, error)
		GetUserByEmail(ctx context.Context, email string) (postgres.User, error)
		GetUserByID(ctx context.Context, id uuid.UUID) (postgres.User, error)
		GetUsersWithSimilar(ctx context.Context, search string) ([]postgres.GetUsersWithSimilarRow, error)

		SetLastSeen(ctx context.Context, id uuid.UUID) error

		UpdateUserEmail(ctx context.Context, arg postgres.UpdateUserEmailParams) error
		UpdateUserGoogleID(ctx context.Context, arg postgres.UpdateUserGoogleIDParams) error
		UpdateUserName(ctx context.Context, arg postgres.UpdateUserNameParams) error
		UpdateUserPassword(ctx context.Context, arg postgres.UpdateUserPasswordParams) error
		UpdateUserPicture(ctx context.Context, arg postgres.UpdateUserPictureParams) error
		UpdateUserRole(ctx context.Context, arg postgres.UpdateUserRoleParams) error

		DeleteUser(ctx context.Context, id uuid.UUID) error
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewUserService(deps Dependencies) *UserService {

	return &UserService{
		repository: deps.Repository,
	}
}

func (s *UserService) CreateUser(ctx context.Context, newUser *model.User) (*model.User, error) {
	// Check if no users so create admin
	users, err := s.repository.GetAllUsers(ctx)
	if err != nil {
		return nil, err
	}

	if len(users) == 0 {
		newUser.Role = model.AdministratorRole
	}

	newUser.ID = uuid.Must(uuid.NewV7())

	if err = s.repository.CreateUser(ctx, postgres.CreateUserParams{
		ID: newUser.ID,
		GoogleID: sql.NullString{
			String: newUser.GoogleID,
			Valid:  newUser.GoogleID != "",
		},
		Email:          newUser.Email,
		Name:           newUser.Name,
		HashedPassword: newUser.HashedPassword,
		Picture:        newUser.Picture,
		Role:           newUser.Role,
	}); err != nil {
		return nil, err
	}
	return newUser, nil
}

func (s *UserService) GetUsers(ctx context.Context, search string) ([]*model.UserInfo, error) {
	result := make([]*model.UserInfo, 0)
	if search == "" {
		users, err := s.repository.GetAllUsers(ctx)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return result, model.ErrNotFound
			}
			return nil, err
		}

		for _, u := range users {
			result = append(result, &model.UserInfo{
				ID:       u.ID,
				Name:     u.Name,
				Picture:  u.Picture,
				Email:    u.Email,
				Role:     u.Role,
				LastSeen: u.LastSeen,
			})

		}
		return result, nil
	} else {
		users, err := s.repository.GetUsersWithSimilar(ctx, search)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return result, model.ErrNotFound
			}
			return nil, err
		}

		for _, u := range users {
			result = append(result, &model.UserInfo{
				ID:       u.ID,
				Name:     u.Name,
				Picture:  u.Picture,
				Email:    u.Email,
				Role:     u.Role,
				LastSeen: u.LastSeen,
			})

		}
		return result, nil
	}
}

func (s *UserService) GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	u, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrNotFound
		}

		return nil, err
	}

	return &model.User{
		ID:             u.ID,
		GoogleID:       u.GoogleID.String,
		Email:          u.Email,
		Name:           u.Name,
		HashedPassword: u.HashedPassword,
		Picture:        u.Picture,
		Role:           u.Role,
		LastSeen:       u.LastSeen,
	}, nil
}

func (s *UserService) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	u, err := s.repository.GetUserByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrNotFound
		}

		return nil, err
	}

	return &model.User{
		ID:             u.ID,
		GoogleID:       u.GoogleID.String,
		Email:          u.Email,
		Name:           u.Name,
		HashedPassword: u.HashedPassword,
		Picture:        u.Picture,
		Role:           u.Role,
		LastSeen:       u.LastSeen,
	}, nil
}

func (s *UserService) UpdateUserEmail(ctx context.Context, user *model.User) error {
	return s.repository.UpdateUserEmail(ctx, postgres.UpdateUserEmailParams{
		ID:    user.ID,
		Email: user.Email,
	})
}

func (s *UserService) UpdateUserPicture(ctx context.Context, user *model.User) error {
	return s.repository.UpdateUserPicture(ctx, postgres.UpdateUserPictureParams{
		ID:      user.ID,
		Picture: user.Picture,
	})
}

func (s *UserService) UpdateUserGoogleID(ctx context.Context, user *model.User) error {
	return s.repository.UpdateUserGoogleID(ctx, postgres.UpdateUserGoogleIDParams{
		ID: user.ID,
		GoogleID: sql.NullString{
			String: user.GoogleID,
			Valid:  user.GoogleID != "",
		},
	})
}

func (s *UserService) UpdateUserPassword(ctx context.Context, user *model.User) error {
	return s.repository.UpdateUserPassword(ctx, postgres.UpdateUserPasswordParams{
		ID:             user.ID,
		HashedPassword: user.HashedPassword,
	})
}

func (s *UserService) UpdateUserRole(ctx context.Context, user *model.User) error {
	return s.repository.UpdateUserRole(ctx, postgres.UpdateUserRoleParams{
		ID:   user.ID,
		Role: user.Role,
	})
}

func (s *UserService) DeleteUser(ctx context.Context, id uuid.UUID) error {
	return s.repository.DeleteUser(ctx, id)
}
