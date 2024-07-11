package model

import (
	"github.com/gofrs/uuid"
	"time"
)

type (
	Exercise struct {
		ID          uuid.UUID    `validate:"omitempty,uuid"`
		CategoryID  uuid.UUID    `validate:"required,uuid"`
		Name        string       `validate:"required,min=3,max=50"`
		Description string       `validate:"omitempty"`
		Data        ExerciseData `validate:"required"`
		CreatedAt   time.Time
	}

	ExerciseData struct {
		Tasks     []Task     `validate:"required,min=1"`
		Instances []Instance `validate:"required,min=0"`
	}

	Task struct {
		ID          uuid.UUID `validate:"required,uuid"`
		Name        string    `validate:"required,min=3,max=50"`
		Description string    `validate:"required,min=1"`
		Points      int32     `validate:"required,min=1,max=1000"`

		LinkedInstanceID uuid.NullUUID `validate:"omitempty,uuid"`
		InstanceFlagVar  string        `validate:"omitempty"`

		Flags []string `validate:"required,min=0"` // len(0) - random, len(1) - static, len(>1) - from list
	}

	Instance struct {
		ID   uuid.UUID `validate:"required,uuid"`
		Name string    `validate:"required,min=3,max=50"`

		Image string `validate:"required,min=1"`

		LinkedTaskID    uuid.NullUUID `validate:"omitempty,uuid"`
		InstanceFlagVar string        `validate:"omitempty"`

		EnvVars    []EnvVar    `validate:"required,min=0"`
		DNSRecords []DNSRecord `validate:"required,min=0"`
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

	ExerciseCategory struct {
		ID          uuid.UUID `validate:"omitempty,uuid"`
		Name        string    `validate:"required,min=3,max=50"`
		Description string    `validate:"omitempty"`
		CreatedAt   time.Time
	}
)
