package model

import (
	"github.com/cybericebox/daemon/internal/appError"
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
		CreatedAt   time.Time
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

	ExerciseCategory struct {
		ID          uuid.UUID `validate:"omitempty,uuid"`
		Name        string    `validate:"required,min=3,max=50"`
		Description string    `validate:"omitempty"`
		CreatedAt   time.Time
	}
)

var (
	ErrExerciseCategory = appError.ErrInternal.WithObjectCode(exerciseCategoryObjectCode)

	ErrExerciseCategoryCategoryExists = appError.ErrObjectExists.WithObjectCode(exerciseCategoryObjectCode).WithMessage("Exercise category already exists")

	ErrExerciseCategoryCategoryNotFound = appError.ErrObjectNotFound.WithObjectCode(exerciseCategoryObjectCode).WithMessage("Exercise category not found")

	ErrExercise = appError.ErrInternal.WithObjectCode(exerciseObjectCode)

	ErrExerciseExerciseNotFound = appError.ErrObjectNotFound.WithObjectCode(exerciseObjectCode).WithMessage("Exercise not found")

	ErrExerciseExerciseExists = appError.ErrObjectExists.WithObjectCode(exerciseObjectCode).WithMessage("Exercise already exists")
)
