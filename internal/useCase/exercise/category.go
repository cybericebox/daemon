package exercise

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

type (
	IExerciseCategoryService interface {
		GetExerciseCategories(ctx context.Context, page, pageSize int) ([]*exerciseModel.ExerciseCategory, error)
		CreateExerciseCategory(ctx context.Context, category exerciseModel.ExerciseCategory) error
		UpdateExerciseCategory(ctx context.Context, category exerciseModel.ExerciseCategory) error
		DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error
	}
)

func (u *ExerciseUseCase) GetExerciseCategories(
	ctx context.Context,
	page, pageSize int,
) ([]*exerciseModel.ExerciseCategory, error) {
	categories, err := u.service.GetExerciseCategories(ctx, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise categories").Err()
	}
	return categories, nil
}

func (u *ExerciseUseCase) CreateExerciseCategory(ctx context.Context, category exerciseModel.ExerciseCategory) error {
	if err := u.service.CreateExerciseCategory(ctx, category); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create exercise category").Err()
	}
	return nil
}

func (u *ExerciseUseCase) UpdateExerciseCategory(ctx context.Context, category exerciseModel.ExerciseCategory) error {
	if err := u.service.UpdateExerciseCategory(ctx, category); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update exercise category").Err()
	}
	return nil
}

func (u *ExerciseUseCase) DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error {
	if err := u.service.DeleteExerciseCategory(ctx, categoryID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete exercise category").Err()
	}
	return nil
}
