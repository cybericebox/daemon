package exercise

import (
	"context"
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
	return u.service.GetExerciseCategories(ctx)
}

func (u *ExerciseUseCase) CreateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error {
	return u.service.CreateExerciseCategory(ctx, category)
}

func (u *ExerciseUseCase) UpdateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error {
	return u.service.UpdateExerciseCategory(ctx, category)
}

func (u *ExerciseUseCase) DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error {
	return u.service.DeleteExerciseCategory(ctx, categoryID)
}
