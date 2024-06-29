package model

import (
	"github.com/gofrs/uuid"
	"time"
)

type (
	File struct {
		ID uuid.UUID

		Name string

		CreatedAt time.Time
	}
)

// constants for file

// Storage types
const (
	BannerStorageType  = "banner"
	TaskStorageType    = "task"
	ProfileStorageType = "profile"
)
