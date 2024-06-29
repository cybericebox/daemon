package model

import (
	"github.com/gofrs/uuid"
	"time"
)

type (
	Exercise struct {
		ID          uuid.UUID
		CategoryID  uuid.UUID
		Name        string
		Description string
		Data        ExerciseData
		CreatedAt   time.Time
	}

	ExerciseData struct {
		Tasks     []Task
		Instances []Instance
	}

	Task struct {
		ID          uuid.UUID
		Name        string
		Description string
		Points      int32

		LinkedInstanceID uuid.NullUUID
		InstanceFlagVar  string

		Flags []string // len(0) - random, len(1) - static, len(>1) - from list
	}

	Instance struct {
		ID   uuid.UUID
		Name string

		Image string

		LinkedTaskID    uuid.NullUUID
		InstanceFlagVar string

		EnvVars    []EnvVar
		DNSRecords []DNSRecord
	}

	EnvVar struct {
		Name  string
		Value string
	}

	DNSRecord struct {
		Type  string
		Name  string
		Value string
	}

	ExerciseCategory struct {
		ID          uuid.UUID
		Name        string
		Description string
		CreatedAt   time.Time
	}
)
