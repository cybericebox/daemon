package model

import (
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/gofrs/uuid"
	"time"
)

type (
	UploadFileData struct {
		FileID      uuid.UUID
		UploadURL   string
		DownloadURL string
	}

	DownloadFileParams struct {
		StorageType string
		FileID      uuid.UUID
		FileName    string
		Expires     time.Duration
	}

	File struct {
		ID          uuid.UUID
		StorageType string
		CreatedAt   time.Time
	}
)

// errors for file
var (
	ErrStorage = appError.ErrInternal.WithObjectCode(storageObjectCode)

	ErrStorageTemporalFileNotFound = appError.ErrObjectNotFound.WithObjectCode(storageObjectCode).WithDetailCode(1).WithMessage("Temporal file not found")
)

// constants for file

// Storage types
const (
	BannerStorageType  = "banner"
	TaskStorageType    = "task"
	ProfileStorageType = "profile"
)
