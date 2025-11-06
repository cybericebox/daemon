package exerciseModel

import (
	"time"

	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

type (
	ExerciseCategory struct {
		ID          uuid.UUID `validate:"omitempty,uuid"`
		Name        string    `validate:"required,min=3,max=50"`
		Description string    `validate:"omitempty"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}
)

var (
	ErrExerciseCategoryExists = err.ErrObjectExists.WithObjectCode(model.ExerciseCategoryObjectCode).WithMessage("Exercise category already exists").WithDetailCode(1) // 41201

	ErrExerciseCategoryNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseCategoryObjectCode).WithMessage("Exercise category not found").WithDetailCode(1) // 31201

	ErrExerciseCategoryHasExercises = err.ErrConflict.WithObjectCode(model.ExerciseCategoryObjectCode).WithMessage("Exercise Category has exercises").WithDetailCode(1) // 71201
	ErrExerciseCategoryDataStale    = err.ErrConflict.WithObjectCode(model.ExerciseCategoryObjectCode).WithMessage("Exercise Category data is stale").WithDetailCode(2) // 71202
)
