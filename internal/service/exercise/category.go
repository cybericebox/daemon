package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	IExerciseCategoryRepository interface {
		CreateExerciseCategory(ctx context.Context, arg postgres.CreateExerciseCategoryParams) error

		GetExerciseCategories(ctx context.Context) ([]postgres.ExerciseCategory, error)

		UpdateExerciseCategory(ctx context.Context, arg postgres.UpdateExerciseCategoryParams) error

		DeleteExerciseCategory(ctx context.Context, id uuid.UUID) error
	}
)

func (s *ExerciseService) GetExerciseCategories(ctx context.Context) ([]*model.ExerciseCategory, error) {
	categories, err := s.repository.GetExerciseCategories(ctx)
	if err != nil {
		return nil, err
	}

	result := make([]*model.ExerciseCategory, 0, len(categories))
	for _, category := range categories {
		result = append(result, &model.ExerciseCategory{
			ID:          category.ID,
			Name:        category.Name,
			Description: category.Description,
			CreatedAt:   category.CreatedAt,
		})
	}

	return result, nil
}

func (s *ExerciseService) CreateExerciseCategory(ctx context.Context, category *model.ExerciseCategory) error {
	if err := s.repository.CreateExerciseCategory(ctx, postgres.CreateExerciseCategoryParams{
		ID:          uuid.Must(uuid.NewV7()),
		Name:        category.Name,
		Description: category.Description,
	}); err != nil {
		return err
	}

	return nil
}

func (s *ExerciseService) UpdateExerciseCategory(ctx context.Context, category *model.ExerciseCategory) error {
	if err := s.repository.UpdateExerciseCategory(ctx, postgres.UpdateExerciseCategoryParams{
		ID:          category.ID,
		Name:        category.Name,
		Description: category.Description,
	}); err != nil {
		return err
	}

	return nil
}

func (s *ExerciseService) DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error {
	if err := s.repository.DeleteExerciseCategory(ctx, categoryID); err != nil {
		return err
	}

	return nil
}
