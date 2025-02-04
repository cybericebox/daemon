package exerciseModel

import (
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"
	"time"
)

type (
	Exercise struct {
		ID          uuid.UUID    `validate:"omitempty,uuid"`
		CategoryID  uuid.UUID    `validate:"required,uuid"`
		Name        string       `validate:"required,min=3,max=50"`
		Description string       `validate:"omitempty,min=1,max=5000"`
		Data        ExerciseData `validate:"required"`

		UpdatedAt time.Time
		UpdatedBy uuid.NullUUID

		CreatedAt time.Time
	}

	ExerciseData struct {
		Tasks     []Task         `validate:"required,min=1"`
		Instances []Instance     `validate:"required,min=0"`
		Files     []ExerciseFile `validate:"required,min=0"`
	}

	Task struct {
		ID          uuid.UUID `validate:"required,uuid"`
		Name        string    `validate:"required,min=3,max=50"`
		Description string    `validate:"required,min=1,max=5000"`
		Points      int32     `validate:"required,min=1,max=1000"`

		AttachedFileIDs []uuid.UUID `validate:"omitempty,dive,uuid"`

		LinkedInstanceID uuid.NullUUID `validate:"omitempty,uuid"`
		InstanceFlagVar  string        `validate:"omitempty"`

		Flags []string `validate:"required,min=0"` // len(0) - random, len(1) - static, len(>1) - from list
	}

	Instance struct {
		ID   uuid.UUID `validate:"required,uuid"`
		Name string    `validate:"required,min=3,max=50"`

		Image string `validate:"required,min=1"`

		EnvVars    []EnvVar    `validate:"required,min=0"`
		DNSRecords []DNSRecord `validate:"required,min=0"`

		LinkedTaskID    uuid.NullUUID `validate:"omitempty,uuid"`
		InstanceFlagVar string        `validate:"omitempty"`
	}

	EnvVar struct {
		Name  string `validate:"required,min=1"`
		Value string `validate:"required,min=1"`
	}

	DNSRecord struct {
		Type  string `validate:"required"`
		Name  string `validate:"required"`
		Value string `validate:"excluded_if=Type A"`
	}

	ExerciseFile struct {
		ID   uuid.UUID `validate:"required,uuid"`
		Name string    `validate:"required,min=3,max=50"`
	}
)

var (
	ErrExercise = err.ErrInternal.WithObjectCode(model.ExerciseObjectCode)

	ErrExerciseExerciseNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).WithMessage("Exercise not found").WithDetailCode(1) // 31101

	ErrExerciseExerciseExists = err.ErrObjectExists.WithObjectCode(model.ExerciseObjectCode).WithMessage("Exercise already exists").WithDetailCode(1) // 41101

	ErrExerciseExerciseInUse     = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).WithMessage("Exercise is in use").WithDetailCode(1)     // 71101
	ErrExerciseExerciseDataStale = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).WithMessage("Exercise data is stale").WithDetailCode(2) // 71102
)
