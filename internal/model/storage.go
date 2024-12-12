package model

import (
	"github.com/cybericebox/lib/pkg/err"
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
	ErrStorage = err.ErrInternal.WithObjectCode(storageObjectCode)

	ErrStorageTemporalFileNotFound = err.ErrObjectNotFound.WithObjectCode(storageObjectCode).WithDetailCode(1).WithMessage("Temporal file not found") // 30401
)

// constants for file

// Storage types
const (
	BannerStorageType  = "banner"
	TaskStorageType    = "task"
	ProfileStorageType = "profile"
)
