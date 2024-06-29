package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	ExerciseUseCase struct {
		service IExerciseService
	}

	IExerciseService interface {
		IExerciseCategoryService

		GetExercises(ctx context.Context) ([]*model.Exercise, error)
		GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error)
		CreateExercise(ctx context.Context, exercise *model.Exercise) error
		UpdateExercise(ctx context.Context, exercise *model.Exercise) error
		DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error
	}

	Dependencies struct {
		Service IExerciseService
	}
)

func NewUseCase(deps Dependencies) *ExerciseUseCase {
	return &ExerciseUseCase{
		service: deps.Service,
	}

}

func (u *ExerciseUseCase) GetExercises(ctx context.Context) ([]*model.Exercise, error) {
	return u.service.GetExercises(ctx)
}

func (u *ExerciseUseCase) GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error) {
	return u.service.GetExercise(ctx, exerciseID)
}

func (u *ExerciseUseCase) CreateExercise(ctx context.Context, exercise *model.Exercise) error {
	return u.service.CreateExercise(ctx, exercise)
}

func (u *ExerciseUseCase) UpdateExercise(ctx context.Context, exercise *model.Exercise) error {
	return u.service.UpdateExercise(ctx, exercise)
}

func (u *ExerciseUseCase) DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error {
	return u.service.DeleteExercise(ctx, exerciseID)
}
