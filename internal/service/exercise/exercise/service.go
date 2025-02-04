package exerciseService

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	ExerciseService struct {
		repository IRepository
	}

	IRepository interface {
		CreateExercise(ctx context.Context, arg postgres.CreateExerciseParams) error

		GetExercises(ctx context.Context, arg postgres.GetExercisesParams) ([]postgres.Exercise, error)
		GetExercisesWithSimilarName(ctx context.Context, arg postgres.GetExercisesWithSimilarNameParams) ([]postgres.Exercise, error)
		GetExercisesByCategory(ctx context.Context, arg postgres.GetExercisesByCategoryParams) ([]postgres.Exercise, error)
		GetExercisesWithIDs(ctx context.Context, ids []uuid.UUID) ([]postgres.Exercise, error)
		GetExercisesNotWithIDs(ctx context.Context, arg postgres.GetExercisesNotWithIDsParams) ([]postgres.Exercise, error)
		GetExercisesNotWithIDsWithSimilarName(ctx context.Context, arg postgres.GetExercisesNotWithIDsWithSimilarNameParams) ([]postgres.Exercise, error)
		GetExerciseByID(ctx context.Context, id uuid.UUID) (postgres.Exercise, error)

		UpdateExercise(ctx context.Context, arg postgres.UpdateExerciseParams) (int64, error)

		DeleteExercise(ctx context.Context, id uuid.UUID) (int64, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *ExerciseService {
	return &ExerciseService{
		repository: deps.Repository,
	}
}

func (s *ExerciseService) GetExercises(ctx context.Context, search string, page int) ([]*exerciseModel.Exercise, error) {
	var err error
	var exercises []postgres.Exercise
	if search == "" {
		exercises, err = s.repository.GetExercises(ctx, postgres.GetExercisesParams{
			Limit:  config.DefaultOnePageLimit,
			Offset: int32(page * config.DefaultOnePageLimit),
		})
	} else {
		exercises, err = s.repository.GetExercisesWithSimilarName(ctx, postgres.GetExercisesWithSimilarNameParams{
			Search: search,
			Limit:  config.DefaultOnePageLimit,
			Offset: int32(page * config.DefaultOnePageLimit),
		})
	}

	if err != nil {
		return nil, exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to get exercises from repository").Err()
	}

	result := make([]*exerciseModel.Exercise, 0, len(exercises))
	for _, exercise := range exercises {
		result = append(result, &exerciseModel.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        exercise.Data,
			UpdatedAt:   exercise.UpdatedAt.Time,
			UpdatedBy:   exercise.UpdatedBy,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercisesByCategory(ctx context.Context, categoryID uuid.UUID, page int) ([]*exerciseModel.Exercise, error) {
	exercises, err := s.repository.GetExercisesByCategory(ctx, postgres.GetExercisesByCategoryParams{
		CategoryID: categoryID,
		Limit:      config.DefaultOnePageLimit,
		Offset:     int32(page * config.DefaultOnePageLimit),
	})
	if err != nil {
		return nil, exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to get exercises from repository").Err()
	}

	result := make([]*exerciseModel.Exercise, 0, len(exercises))
	for _, exercise := range exercises {
		result = append(result, &exerciseModel.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        exercise.Data,
			UpdatedAt:   exercise.UpdatedAt.Time,
			UpdatedBy:   exercise.UpdatedBy,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercisesWithIDs(ctx context.Context, exerciseIDs []uuid.UUID) ([]*exerciseModel.Exercise, error) {
	exercises, err := s.repository.GetExercisesWithIDs(ctx, exerciseIDs)
	if err != nil {
		return nil, exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to get exercises from repository").WithContext("exerciseIDs", exerciseIDs).Err()
	}

	result := make([]*exerciseModel.Exercise, 0, len(exercises))
	for _, exercise := range exercises {
		result = append(result, &exerciseModel.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        exercise.Data,
			UpdatedAt:   exercise.UpdatedAt.Time,
			UpdatedBy:   exercise.UpdatedBy,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercisesNotWithIDs(ctx context.Context, exerciseIDs []uuid.UUID, search string, page int) ([]*exerciseModel.Exercise, error) {
	var err error
	var exercises []postgres.Exercise
	if search == "" {
		exercises, err = s.repository.GetExercisesNotWithIDs(ctx, postgres.GetExercisesNotWithIDsParams{
			Limit:  config.DefaultOnePageLimit,
			Offset: int32(page * config.DefaultOnePageLimit),
			Ids:    exerciseIDs,
		})
	} else {
		exercises, err = s.repository.GetExercisesNotWithIDsWithSimilarName(ctx, postgres.GetExercisesNotWithIDsWithSimilarNameParams{
			Search: search,
			Limit:  config.DefaultOnePageLimit,
			Offset: int32(page * config.DefaultOnePageLimit),
			Ids:    exerciseIDs,
		})
	}

	if err != nil {
		return nil, exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to get exercises from repository").Err()
	}

	result := make([]*exerciseModel.Exercise, 0, len(exercises))
	for _, exercise := range exercises {
		result = append(result, &exerciseModel.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        exercise.Data,
			UpdatedAt:   exercise.UpdatedAt.Time,
			UpdatedBy:   exercise.UpdatedBy,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercise(ctx context.Context, exerciseID uuid.UUID) (*exerciseModel.Exercise, error) {
	exercise, err := s.repository.GetExerciseByID(ctx, exerciseID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, exerciseModel.ErrExerciseExerciseNotFound.WithContext("exerciseID", exerciseID).Err()
		}
		return nil, exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to get exercise from repository").WithContext("exerciseID", exerciseID).Err()
	}

	return &exerciseModel.Exercise{
		ID:          exercise.ID,
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        exercise.Data,
		UpdatedAt:   exercise.UpdatedAt.Time,
		UpdatedBy:   exercise.UpdatedBy,
		CreatedAt:   exercise.CreatedAt,
	}, nil
}

func (s *ExerciseService) CreateExercise(ctx context.Context, exercise exerciseModel.Exercise) error {
	s.linkTasksToInstances(&exercise.Data)

	createExercise := postgres.CreateExerciseParams{
		ID:          uuid.Must(uuid.NewV7()),
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        exercise.Data,
	}

	if err := s.repository.CreateExercise(ctx, createExercise); err != nil {
		errCreator, has := tools.UniqueViolationError(err, exerciseModel.ErrExerciseExerciseExists)
		if has {
			return errCreator.Err()
		}

		errCreator, has = tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to create exercise").Err()
	}

	return nil
}

func (s *ExerciseService) UpdateExercise(ctx context.Context, exercise exerciseModel.Exercise) error {
	s.linkTasksToInstances(&exercise.Data)

	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	updateExercise := postgres.UpdateExerciseParams{
		ID:          exercise.ID,
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        exercise.Data,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	}

	affected, err := s.repository.UpdateExercise(ctx, updateExercise)
	if err != nil {
		errCreator, has := tools.UniqueViolationError(err, exerciseModel.ErrExerciseExerciseExists)
		if has {
			return errCreator.Err()
		}

		errCreator, has = tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to update exercise").Err()
	}

	if affected == 0 {
		return exerciseModel.ErrExerciseExerciseNotFound.WithContext("exerciseID", exercise.ID).Err()
	}

	return nil
}

func (s *ExerciseService) DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error {
	affected, err := s.repository.DeleteExercise(ctx, exerciseID)
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err, true)
		if has {
			return errCreator.Err()
		}
		return exerciseModel.ErrExercise.WithError(err).WithMessage("Failed to delete exercise").Err()
	}

	if affected == 0 {
		return exerciseModel.ErrExerciseExerciseNotFound.WithContext("exerciseID", exerciseID).Err()
	}

	return nil
}

func (s *ExerciseService) linkTasksToInstances(exerciseData *exerciseModel.ExerciseData) {
	for _, task := range exerciseData.Tasks {
		for i, instance := range exerciseData.Instances {
			if task.LinkedInstanceID.Valid && task.LinkedInstanceID.UUID == instance.ID {
				exerciseData.Instances[i].LinkedTaskID = uuid.NullUUID{
					UUID:  task.ID,
					Valid: true,
				}
				exerciseData.Instances[i].InstanceFlagVar = task.InstanceFlagVar
			}
		}
	}
}
