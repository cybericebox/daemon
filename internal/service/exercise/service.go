package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	ExerciseService struct {
		repository IRepository
	}

	IRepository interface {
		IExerciseCategoryRepository

		CreateExercise(ctx context.Context, arg postgres.CreateExerciseParams) error

		GetExercises(ctx context.Context) ([]postgres.Exercise, error)
		GetExercisesWithSimilarName(ctx context.Context, search string) ([]postgres.Exercise, error)
		GetExercisesByCategory(ctx context.Context, categoryID uuid.UUID) ([]postgres.Exercise, error)
		GetExercisesByIDs(ctx context.Context, ids []uuid.UUID) ([]postgres.Exercise, error)
		GetExerciseByID(ctx context.Context, id uuid.UUID) (postgres.Exercise, error)

		UpdateExercise(ctx context.Context, arg postgres.UpdateExerciseParams) (int64, error)

		DeleteExercise(ctx context.Context, id uuid.UUID) (int64, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewExerciseService(deps Dependencies) *ExerciseService {

	return &ExerciseService{
		repository: deps.Repository,
	}
}

func (s *ExerciseService) GetExercises(ctx context.Context, search string) ([]*model.Exercise, error) {
	var err error
	var exercises []postgres.Exercise
	if search == "" {
		exercises, err = s.repository.GetExercises(ctx)
	} else {
		exercises, err = s.repository.GetExercisesWithSimilarName(ctx, search)
	}

	if err != nil {
		return nil, model.ErrExercise.WithError(err).WithMessage("Failed to get exercises from repository").Cause()
	}

	result := make([]*model.Exercise, 0, len(exercises))
	for _, exercise := range exercises {

		result = append(result, &model.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        exercise.Data,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercisesByCategory(ctx context.Context, categoryID uuid.UUID) ([]*model.Exercise, error) {
	exercises, err := s.repository.GetExercisesByCategory(ctx, categoryID)
	if err != nil {
		return nil, model.ErrExercise.WithError(err).WithMessage("Failed to get exercises from repository").Cause()
	}

	result := make([]*model.Exercise, 0, len(exercises))
	for _, exercise := range exercises {

		result = append(result, &model.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        exercise.Data,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercisesByIDs(ctx context.Context, exerciseIDs []uuid.UUID) ([]*model.Exercise, error) {
	exercises, err := s.repository.GetExercisesByIDs(ctx, exerciseIDs)
	if err != nil {
		return nil, model.ErrExercise.WithError(err).WithMessage("Failed to get exercises from repository").WithContext("exerciseIDs", exerciseIDs).Cause()
	}

	result := make([]*model.Exercise, 0, len(exercises))
	for _, exercise := range exercises {

		result = append(result, &model.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        exercise.Data,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error) {
	exercise, err := s.repository.GetExerciseByID(ctx, exerciseID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrExerciseExerciseNotFound.WithContext("exerciseID", exerciseID).Cause()
		}
		return nil, model.ErrExercise.WithError(err).WithMessage("Failed to get exercise from repository").WithContext("exerciseID", exerciseID).Cause()
	}

	return &model.Exercise{
		ID:          exercise.ID,
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        exercise.Data,
		CreatedAt:   exercise.CreatedAt,
	}, nil
}

func (s *ExerciseService) CreateExercise(ctx context.Context, exercise model.Exercise) error {
	s.linkTasksToInstances(&exercise.Data)

	createExercise := postgres.CreateExerciseParams{
		ID:          uuid.Must(uuid.NewV7()),
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        exercise.Data,
	}

	if err := s.repository.CreateExercise(ctx, createExercise); err != nil {
		if tools.IsUniqueViolationError(err) {
			return model.ErrExerciseExerciseExists.Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrExercise.WithError(err).WithMessage("Failed to create exercise").Cause()
	}

	return nil
}

func (s *ExerciseService) UpdateExercise(ctx context.Context, exercise model.Exercise) error {
	s.linkTasksToInstances(&exercise.Data)

	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
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
		if tools.IsUniqueViolationError(err) {
			return model.ErrExerciseExerciseExists.Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrExercise.WithError(err).WithMessage("Failed to update exercise").Cause()
	}

	if affected == 0 {
		return model.ErrExerciseExerciseNotFound.WithContext("exerciseID", exercise.ID).Cause()
	}

	return nil
}

func (s *ExerciseService) DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error {
	affected, err := s.repository.DeleteExercise(ctx, exerciseID)
	if err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to delete exercise").Cause()
	}

	if affected == 0 {
		return model.ErrExerciseExerciseNotFound.WithContext("exerciseID", exerciseID).Cause()
	}

	return nil
}

func (s *ExerciseService) linkTasksToInstances(exerciseData *model.ExerciseData) {
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
