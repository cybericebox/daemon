package user

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/user"
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

		GetAllUsers(ctx context.Context, arg postgres.GetAllUsersParams) ([]postgres.GetAllUsersRow, error)
		GetUserByEmail(ctx context.Context, email string) (postgres.User, error)
		GetUserByID(ctx context.Context, id uuid.UUID) (postgres.User, error)
		GetUsersWithSimilar(ctx context.Context, arg postgres.GetUsersWithSimilarParams) ([]postgres.GetUsersWithSimilarRow, error)

		GetUsersWithEmails(ctx context.Context, emails []string) ([]postgres.User, error)

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

func (s *UserService) CreateUser(ctx context.Context, newUser userModel.User) (*userModel.User, error) {
	// Check if no users so create admin
	usersCount, err := s.repository.CountUsers(ctx)
	if err != nil {
		return nil, userModel.ErrUser.WithError(err).WithMessage("Failed to count users").Err()
	}

	if usersCount == 0 {
		newUser.Role = userModel.AdministratorRole
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
			return nil, userModel.ErrUserUserExists.WithContext("email", newUser.Email).Err()
		}
		return nil, userModel.ErrUser.WithError(err).WithMessage("Failed to create user").Err()
	}
	return &newUser, nil
}

func (s *UserService) GetUsers(ctx context.Context, search string, page int) ([]*userModel.UserInfo, error) {

	result := make([]*userModel.UserInfo, 0)
	if search == "" {
		users, err := s.repository.GetAllUsers(ctx, postgres.GetAllUsersParams{
			Limit:  int32(config.DefaultOnePageLimit),
			Offset: int32(page * config.DefaultOnePageLimit),
		})
		if err != nil {
			return nil, userModel.ErrUser.WithError(err).WithMessage("Failed to get all users from db").Err()
		}

		for _, u := range users {
			result = append(result, &userModel.UserInfo{
				ID:            u.ID,
				ConnectGoogle: u.GoogleID.Valid,
				Name:          u.Name,
				Picture:       u.Picture,
				Email:         u.Email,
				Role:          u.Role,
				LastSeen:      u.LastSeen,
				CreatedAt:     u.CreatedAt,
				UpdatedAt:     u.UpdatedAt.Time,
				UpdatedBy:     u.UpdatedBy,
			})

		}
		return result, nil
	} else {
		users, err := s.repository.GetUsersWithSimilar(ctx, postgres.GetUsersWithSimilarParams{
			Search: search,
			Limit:  int32(config.DefaultOnePageLimit),
			Offset: int32(page * config.DefaultOnePageLimit),
		})
		if err != nil {
			return nil, userModel.ErrUser.WithError(err).WithMessage("Failed to get users with similar from db").Err()
		}

		for _, u := range users {
			result = append(result, &userModel.UserInfo{
				ID:            u.ID,
				ConnectGoogle: u.GoogleID.Valid,
				Name:          u.Name,
				Picture:       u.Picture,
				Email:         u.Email,
				Role:          u.Role,
				LastSeen:      u.LastSeen,
				UpdatedAt:     u.UpdatedAt.Time,
				UpdatedBy:     u.UpdatedBy,
				CreatedAt:     u.CreatedAt,
			})

		}
		return result, nil
	}
}

func (s *UserService) GetUserByID(ctx context.Context, userID uuid.UUID) (*userModel.User, error) {
	u, err := s.repository.GetUserByID(ctx, userID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, userModel.ErrUserUserNotFound.WithContext("userID", userID).Err()
		}

		return nil, userModel.ErrUser.WithError(err).WithMessage("Failed to get user by id from db").Err()
	}

	return &userModel.User{
		ID:             u.ID,
		GoogleID:       u.GoogleID.String,
		Email:          u.Email,
		Name:           u.Name,
		HashedPassword: u.HashedPassword,
		Picture:        u.Picture,
		Role:           u.Role,
		LastSeen:       u.LastSeen,
		UpdatedAt:      u.UpdatedAt.Time,
		UpdatedBy:      u.UpdatedBy,
		CreatedAt:      u.CreatedAt,
	}, nil
}

func (s *UserService) GetUserByEmail(ctx context.Context, email string) (*userModel.User, error) {
	u, err := s.repository.GetUserByEmail(ctx, email)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, userModel.ErrUserUserNotFound.WithContext("email", email).Err()
		}

		return nil, userModel.ErrUser.WithError(err).WithMessage("Failed to get user by email from db").Err()
	}

	return &userModel.User{
		ID:             u.ID,
		GoogleID:       u.GoogleID.String,
		Email:          u.Email,
		Name:           u.Name,
		HashedPassword: u.HashedPassword,
		Picture:        u.Picture,
		Role:           u.Role,
		LastSeen:       u.LastSeen,
		UpdatedAt:      u.UpdatedAt.Time,
		UpdatedBy:      u.UpdatedBy,
		CreatedAt:      u.CreatedAt,
	}, nil
}

func (s *UserService) GetExistingUsersEmails(ctx context.Context, emails []string) ([]string, error) {
	users, err := s.repository.GetUsersWithEmails(ctx, emails)
	if err != nil {
		return nil, userModel.ErrUser.WithError(err).WithMessage("Failed to get users by emails from db").Err()
	}

	existingEmails := make([]string, 0)
	for _, u := range users {
		existingEmails = append(existingEmails, u.Email)
	}

	return existingEmails, nil
}

func (s *UserService) UpdateUserEmail(ctx context.Context, user userModel.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
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
			return errCreator.Err()
		}
		return userModel.ErrUser.WithError(err).WithMessage("Failed to update user email in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", user.ID).Err()
	}

	return nil
}

func (s *UserService) UpdateUserName(ctx context.Context, user userModel.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
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
			return errCreator.Err()
		}
		return userModel.ErrUser.WithError(err).WithMessage("Failed to update user name in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", user.ID).Err()
	}

	return nil
}

func (s *UserService) UpdateUserPicture(ctx context.Context, user userModel.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
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
			return errCreator.Err()
		}
		return userModel.ErrUser.WithError(err).WithMessage("Failed to update user picture in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", user.ID).Err()
	}

	return nil
}

func (s *UserService) UpdateUserGoogleID(ctx context.Context, user userModel.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
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
			return errCreator.Err()
		}
		return userModel.ErrUser.WithError(err).WithMessage("Failed to update user google id in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", user.ID).Err()
	}

	return nil
}

func (s *UserService) UpdateUserPassword(ctx context.Context, user userModel.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
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
			return errCreator.Err()
		}
		return userModel.ErrUser.WithError(err).WithMessage("Failed to update user password in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", user.ID).Err()
	}

	return nil
}

func (s *UserService) UpdateUserRole(ctx context.Context, user userModel.User) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
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
			return errCreator.Err()
		}
		return userModel.ErrUser.WithError(err).WithMessage("Failed to update user role in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", user.ID).Err()
	}

	return nil
}

func (s *UserService) DeleteUser(ctx context.Context, id uuid.UUID) error {
	affected, err := s.repository.DeleteUser(ctx, id)
	if err != nil {
		return userModel.ErrUser.WithError(err).WithMessage("Failed to delete user in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", id).Err()
	}
	return nil
}

func (s *UserService) SetLastSeen(ctx context.Context, id uuid.UUID) error {
	affected, err := s.repository.SetLastSeen(ctx, id)
	if err != nil {
		return userModel.ErrUser.WithError(err).WithMessage("Failed to set last seen in db").Err()
	}
	if affected == 0 {
		return userModel.ErrUserUserNotFound.WithContext("userID", id).Err()
	}
	return nil
}
