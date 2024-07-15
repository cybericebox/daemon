package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	IExerciseCategoryService interface {
		GetExerciseCategories(ctx context.Context) ([]*model.ExerciseCategory, error)
		CreateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error
		UpdateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error
		DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error
	}
)

func (u *ExerciseUseCase) GetExerciseCategories(ctx context.Context) ([]*model.ExerciseCategory, error) {
	categories, err := u.service.GetExerciseCategories(ctx)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get exercise categories")
	}
	return categories, nil
}

func (u *ExerciseUseCase) CreateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error {
	if err := u.service.CreateExerciseCategory(ctx, category); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to create exercise category")
	}
	return nil
}

func (u *ExerciseUseCase) UpdateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error {
	if err := u.service.UpdateExerciseCategory(ctx, category); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to update exercise category")
	}
	return nil
}

func (u *ExerciseUseCase) DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error {
	if err := u.service.DeleteExerciseCategory(ctx, categoryID); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete exercise category")
	}
	return nil
}
