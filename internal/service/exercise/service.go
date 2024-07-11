package exercise

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
)

type (
	ExerciseService struct {
		repository IRepository
	}

	IRepository interface {
		IExerciseCategoryRepository

		CreateExercise(ctx context.Context, arg postgres.CreateExerciseParams) error

		GetExercises(ctx context.Context) ([]postgres.Exercise, error)
		GetExercisesByCategory(ctx context.Context, categoryID uuid.UUID) ([]postgres.Exercise, error)
		GetExerciseByID(ctx context.Context, id uuid.UUID) (postgres.Exercise, error)

		UpdateExercise(ctx context.Context, arg postgres.UpdateExerciseParams) error

		DeleteExercise(ctx context.Context, id uuid.UUID) error
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

func (s *ExerciseService) GetExercises(ctx context.Context) ([]*model.Exercise, error) {
	exercises, err := s.repository.GetExercises(ctx)
	if err != nil {
		return nil, err
	}

	var errs error

	result := make([]*model.Exercise, 0, len(exercises))
	for _, exercise := range exercises {

		data, err := s.convertToModelData(exercise.Data)
		if err != nil {
			errs = multierror.Append(errs, err)
			continue
		}

		result = append(result, &model.Exercise{
			ID:          exercise.ID,
			CategoryID:  exercise.CategoryID,
			Name:        exercise.Name,
			Description: exercise.Description,
			Data:        data,
			CreatedAt:   exercise.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error) {
	exercise, err := s.repository.GetExerciseByID(ctx, exerciseID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}

	data, err := s.convertToModelData(exercise.Data)
	if err != nil {
		return nil, err
	}

	return &model.Exercise{
		ID:          exercise.ID,
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        data,
		CreatedAt:   exercise.CreatedAt,
	}, nil
}

func (s *ExerciseService) CreateExercise(ctx context.Context, exercise model.Exercise) error {
	//TODO: check if exercise exists by name

	for _, task := range exercise.Data.Tasks {
		for i, instance := range exercise.Data.Instances {
			if task.LinkedInstanceID.Valid && task.LinkedInstanceID.UUID == instance.ID {
				exercise.Data.Instances[i].LinkedTaskID = uuid.NullUUID{
					UUID:  task.ID,
					Valid: true,
				}
				exercise.Data.Instances[i].InstanceFlagVar = task.InstanceFlagVar
			}
		}
	}

	data, err := s.convertToJSON(exercise.Data)
	if err != nil {
		return err
	}

	createExercise := postgres.CreateExerciseParams{
		ID:          uuid.Must(uuid.NewV7()),
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        data,
	}

	if err = s.repository.CreateExercise(ctx, createExercise); err != nil {
		return err
	}

	return nil
}

func (s *ExerciseService) UpdateExercise(ctx context.Context, exercise model.Exercise) error {
	for _, task := range exercise.Data.Tasks {
		for i, instance := range exercise.Data.Instances {
			if task.LinkedInstanceID.Valid && task.LinkedInstanceID.UUID == instance.ID {
				exercise.Data.Instances[i].LinkedTaskID = uuid.NullUUID{
					UUID:  task.ID,
					Valid: true,
				}
				exercise.Data.Instances[i].InstanceFlagVar = task.InstanceFlagVar
			}
		}
	}

	data, err := s.convertToJSON(exercise.Data)
	if err != nil {
		return err
	}

	updateExercise := postgres.UpdateExerciseParams{
		ID:          exercise.ID,
		CategoryID:  exercise.CategoryID,
		Name:        exercise.Name,
		Description: exercise.Description,
		Data:        data,
	}

	if err = s.repository.UpdateExercise(ctx, updateExercise); err != nil {
		return err
	}

	return nil
}

func (s *ExerciseService) DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error {
	if err := s.repository.DeleteExercise(ctx, exerciseID); err != nil {
		return err
	}

	return nil
}

func (s *ExerciseService) convertToModelData(data json.RawMessage) (model.ExerciseData, error) {
	var modelData model.ExerciseData
	if err := json.Unmarshal(data, &modelData); err != nil {
		return model.ExerciseData{}, err
	}

	return modelData, nil
}

func (s *ExerciseService) convertToJSON(data model.ExerciseData) (json.RawMessage, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}
