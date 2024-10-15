package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	IExerciseCategoryRepository interface {
		CreateExerciseCategory(ctx context.Context, arg postgres.CreateExerciseCategoryParams) error

		GetExerciseCategories(ctx context.Context) ([]postgres.ExerciseCategory, error)

		UpdateExerciseCategory(ctx context.Context, arg postgres.UpdateExerciseCategoryParams) (int64, error)

		DeleteExerciseCategory(ctx context.Context, id uuid.UUID) (int64, error)
	}
)

func (s *ExerciseService) GetExerciseCategories(ctx context.Context) ([]*model.ExerciseCategory, error) {
	categories, err := s.repository.GetExerciseCategories(ctx)
	if err != nil {
		return nil, model.ErrExerciseCategory.WithError(err).WithMessage("Failed to get exercise categories").Cause()
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

func (s *ExerciseService) CreateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error {
	if err := s.repository.CreateExerciseCategory(ctx, postgres.CreateExerciseCategoryParams{
		ID:          uuid.Must(uuid.NewV7()),
		Name:        category.Name,
		Description: category.Description,
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return model.ErrExerciseCategoryCategoryExists.Cause()
		}
		return model.ErrExerciseCategory.WithError(err).WithMessage("Failed to create exercise category").Cause()
	}

	return nil
}

func (s *ExerciseService) UpdateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateExerciseCategory(ctx, postgres.UpdateExerciseCategoryParams{
		ID:          category.ID,
		Name:        category.Name,
		Description: category.Description,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		if tools.IsUniqueViolationError(err) {
			return model.ErrExerciseCategoryCategoryExists.Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrExerciseCategory.WithError(err).WithMessage("Failed to update exercise category").WithContext("categoryID", category.ID).Cause()
	}

	if affected == 0 {
		return model.ErrExerciseCategoryCategoryNotFound.WithMessage("Exercise category not found").WithContext("categoryID", category.ID).Cause()
	}

	return nil
}

func (s *ExerciseService) DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error {
	affected, err := s.repository.DeleteExerciseCategory(ctx, categoryID)
	if err != nil {
		return model.ErrExerciseCategory.WithError(err).WithMessage("Failed to delete exercise category").WithContext("categoryID", categoryID).Cause()
	}

	if affected == 0 {
		return model.ErrExerciseCategoryCategoryNotFound.WithMessage("Exercise category not found").WithContext("categoryID", categoryID).Cause()
	}
	return nil
}
