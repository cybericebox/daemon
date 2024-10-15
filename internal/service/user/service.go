package user

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

type (
	UserService struct {
		repository IRepository
	}

	IRepository interface {
		CreateUser(ctx context.Context, arg postgres.CreateUserParams) error

		CountUsers(ctx context.Context) (int64, error)

		GetAllUsers(ctx context.Context) ([]postgres.GetAllUsersRow, error)
		GetUserByEmail(ctx context.Context, email string) (postgres.User, error)
		GetUserByID(ctx context.Context, id uuid.UUID) (postgres.User, error)
		GetUsersWithSimilar(ctx context.Context, search string) ([]postgres.GetUsersWithSimilarRow, error)

		SetLastSeen(ctx context.Context, id uuid.UUID) (int64, error)

		UpdateUserEmail(ctx context.Context, arg postgres.UpdateUserEmailParams) (int64, error)
		UpdateUserGoogleID(ctx context.Context, arg postgres.UpdateUserGoogleIDParams) (int64, error)
		UpdateUserName(ctx context.Context, arg postgres.UpdateUserNameParams) (int64, error)
		UpdateUserPassword(ctx context.Context, arg postgres.UpdateUserPasswordParams) (int64, error)
		UpdateUserPicture(ctx context.Context, arg postgres.UpdateUserPictureParams) (int64, error)
		UpdateUserRole(ctx context.Context, arg postgres.UpdateUserRoleParams) (int64, error)

		DeleteUser(ctx context.Context, id uuid.UUID) (int64, error)
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

func (s *UserService) CreateUser(ctx context.Context, newUser model.User) (*model.User, error) {
	// Check if no users so create admin
	usersCount, err := s.repository.CountUsers(ctx)
	if err != nil {
		return nil, model.ErrUser.WithError(err).WithMessage("Failed to count users").Cause()
	}

	if usersCount == 0 {
		newUser.Role = model.AdministratorRole
	}

	newUser.ID = uuid.Must(uuid.NewV7())

	if err = s.repository.CreateUser(ctx, postgres.CreateUserParams{
		ID: newUser.ID,
		GoogleID: pgtype.Text{
			String: newUser.GoogleID,
			Valid:  newUser.GoogleID != "",
		},
		Email:          newUser.Email,
		Name:           newUser.Name,
		HashedPassword: newUser.HashedPassword,
		Picture:        newUser.Picture,
		Role:           newUser.Role,
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return nil, model.ErrUserUserExists.WithContext("email", newUser.Email).Cause()
		}
		return nil, model.ErrUser.WithError(err).WithMessage("Failed to create user").Cause()
	}
	return &newUser, nil
}

func (s *UserService) GetUsers(ctx context.Context, search string) ([]*model.UserInfo, error) {
	result := make([]*model.UserInfo, 0)
	if search == "" {
		users, err := s.repository.GetAllUsers(ctx)
		if err != nil {
			return nil, model.ErrUser.WithError(err).WithMessage("Failed to get all users from db").Cause()
		}

		for _, u := range users {
			result = append(result, &model.UserInfo{
				ID:            u.ID,
				ConnectGoogle: u.GoogleID.Valid,
				Name:          u.Name,
				Picture:       u.Picture,
				Email:         u.Email,
				Role:          u.Role,
				LastSeen:      u.LastSeen,
				CreatedAt:     u.CreatedAt,
				UpdatedAt:     u.UpdatedAt.Time,
				UpdatedBy:     u.UpdatedBy.UUID,
			})

		}
		return result, nil
	} else {
		users, err := s.repository.GetUsersWithSimilar(ctx, search)
		if err != nil {
			return nil, model.ErrUser.WithError(err).WithMessage("Failed to get users with similar from db").Cause()
		}

		for _, u := range users {
			result = append(result, &model.UserInfo{
				ID:            u.ID,
				ConnectGoogle: u.GoogleID.Valid,
				Name:          u.Name,
				Picture:       u.Picture,
				Email:         u.Email,
				Role:          u.Role,
				LastSeen:      u.LastSeen,
				UpdatedAt:     u.UpdatedAt.Time,
				UpdatedBy:     u.UpdatedBy.UUID,
				CreatedAt:     u.CreatedAt,
			})

		}
		return result, nil
	}
}

func (s *UserService) GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error) {
	u, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrUserUserNotFound.WithContext("userID", userID).Cause()
		}

		return nil, model.ErrUser.WithError(err).WithMessage("Failed to get user by id from db").Cause()
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
		UpdatedAt:      u.UpdatedAt.Time,
		UpdatedBy:      u.UpdatedBy.UUID,
		CreatedAt:      u.CreatedAt,
	}, nil
}

func (s *UserService) GetUserByEmail(ctx context.Context, email string) (*model.User, error) {
	u, err := s.repository.GetUserByEmail(ctx, email)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrUserUserNotFound.WithContext("email", email).Cause()
		}

		return nil, model.ErrUser.WithError(err).WithMessage("Failed to get user by email from db").Cause()
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
		UpdatedAt:      u.UpdatedAt.Time,
		UpdatedBy:      u.UpdatedBy.UUID,
		CreatedAt:      u.CreatedAt,
	}, nil
}

func (s *UserService) UpdateUserEmail(ctx context.Context, user model.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateUserEmail(ctx, postgres.UpdateUserEmailParams{
		ID:    user.ID,
		Email: user.Email,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrUser.WithError(err).WithMessage("Failed to update user email in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", user.ID).Cause()
	}

	return nil
}

func (s *UserService) UpdateUserName(ctx context.Context, user model.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateUserName(ctx, postgres.UpdateUserNameParams{
		ID:   user.ID,
		Name: user.Name,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrUser.WithError(err).WithMessage("Failed to update user name in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", user.ID).Cause()
	}

	return nil
}

func (s *UserService) UpdateUserPicture(ctx context.Context, user model.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateUserPicture(ctx, postgres.UpdateUserPictureParams{
		ID:      user.ID,
		Picture: user.Picture,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrUser.WithError(err).WithMessage("Failed to update user picture in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", user.ID).Cause()
	}

	return nil
}

func (s *UserService) UpdateUserGoogleID(ctx context.Context, user model.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateUserGoogleID(ctx, postgres.UpdateUserGoogleIDParams{
		ID: user.ID,
		GoogleID: pgtype.Text{
			String: user.GoogleID,
			Valid:  user.GoogleID != "",
		},
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrUser.WithError(err).WithMessage("Failed to update user google id in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", user.ID).Cause()
	}

	return nil
}

func (s *UserService) UpdateUserPassword(ctx context.Context, user model.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateUserPassword(ctx, postgres.UpdateUserPasswordParams{
		ID:             user.ID,
		HashedPassword: user.HashedPassword,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrUser.WithError(err).WithMessage("Failed to update user password in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", user.ID).Cause()
	}

	return nil
}

func (s *UserService) UpdateUserRole(ctx context.Context, user model.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateUserRole(ctx, postgres.UpdateUserRoleParams{
		ID:   user.ID,
		Role: user.Role,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrUser.WithError(err).WithMessage("Failed to update user role in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", user.ID).Cause()
	}

	return nil
}

func (s *UserService) DeleteUser(ctx context.Context, id uuid.UUID) error {
	affected, err := s.repository.DeleteUser(ctx, id)
	if err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to delete user in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", id).Cause()
	}
	return nil
}

func (s *UserService) SetLastSeen(ctx context.Context, id uuid.UUID) error {
	affected, err := s.repository.SetLastSeen(ctx, id)
	if err != nil {
		return model.ErrUser.WithError(err).WithMessage("Failed to set last seen in db").Cause()
	}
	if affected == 0 {
		return model.ErrUserUserNotFound.WithContext("userID", id).Cause()
	}
	return nil
}
