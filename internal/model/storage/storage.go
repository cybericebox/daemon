package storageModel

import (
	"time"

	"github.com/cybericebox/lib/pkg/err"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
)

type (
	UploadFileParams struct {
		StorageType string
		Expires     time.Duration
	}

	UploadFileData struct {
		FileID      uuid.UUID
		UploadURL   string
		DownloadURL string
	}

	DownloadFileParams struct {
		StorageType   string
		FileID        uuid.UUID
		FileName      string
		Expires       time.Duration
		WithExtension bool
	}

	DownloadFileURL string

	File struct {
		ID          uuid.UUID
		StorageType string
		CreatedAt   time.Time
	}
)

// errors for file
var (
	ErrStorageTemporalFileNotFound = err.ErrObjectNotFound.WithObjectCode(model.StorageObjectCode).WithDetailCode(1).WithMessage("Temporal file not found") // 30401
	ErrStorageFileNotFound         = err.ErrObjectNotFound.WithObjectCode(model.StorageObjectCode).WithDetailCode(2).WithMessage("File not found")          // 30402
)

// Storage types
const (
	BannerStorageType  = "banner"
	TaskStorageType    = "task"
	ProfileStorageType = "profile"
)
