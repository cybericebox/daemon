package storage

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/gofrs/uuid"
	"time"
)

type (
	StorageService struct {
		config     *config.StorageConfig
		repository IRepository
	}

	IRepository interface {
		CreateFile(ctx context.Context, arg postgres.CreateFileParams) error
		GetFileByID(ctx context.Context, id uuid.UUID) (postgres.File, error)
	}

	Dependencies struct {
		Config     *config.StorageConfig
		Repository IRepository
	}
)

func NewStorageService(deps Dependencies) *StorageService {
	return &StorageService{
		config:     deps.Config,
		repository: deps.Repository,
	}
}

func (s *StorageService) GetUploadFileLink(ctx context.Context, storageType, fileID string, expires ...time.Duration) (string, error) {
	//TODO implement me
	panic("implement me")
}

func (s *StorageService) GetDownloadFileLink(ctx context.Context, storageType, fileID string, expires ...time.Duration) (string, error) {
	//TODO implement me
	panic("implement me")
}
