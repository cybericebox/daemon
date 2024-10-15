package storageS3

import (
	"github.com/cybericebox/daemon/internal/config"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog/log"
)

type (
	StorageS3Repository struct {
		*minio.Client
	}

	Dependencies struct {
		Config *config.StorageS3Config
	}
)

func NewRepository(deps Dependencies) *StorageS3Repository {
	client, err := newStorageS3(deps.Config)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create S3 storage client")
	}

	return &StorageS3Repository{
		client,
	}
}

func newStorageS3(cfg *config.StorageS3Config) (*minio.Client, error) {
	return minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
}
