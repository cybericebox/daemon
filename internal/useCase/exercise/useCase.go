package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
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
		CreateExercise(ctx context.Context, exercise model.Exercise) error
		UpdateExercise(ctx context.Context, exercise model.Exercise) error
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
	exercises, err := u.service.GetExercises(ctx)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get exercises")
	}
	return exercises, nil
}

func (u *ExerciseUseCase) GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error) {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get exercise")
	}
	return exercise, nil
}

func (u *ExerciseUseCase) CreateExercise(ctx context.Context, exercise model.Exercise) error {
	if err := u.service.CreateExercise(ctx, exercise); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to create exercise")
	}
	return nil
}

func (u *ExerciseUseCase) UpdateExercise(ctx context.Context, exercise model.Exercise) error {
	if err := u.service.UpdateExercise(ctx, exercise); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to update exercise")
	}
	return nil
}

func (u *ExerciseUseCase) DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error {
	if err := u.service.DeleteExercise(ctx, exerciseID); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete exercise")
	}
	return nil
}
