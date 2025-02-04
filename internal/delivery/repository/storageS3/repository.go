package storageS3

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	storageModel "github.com/cybericebox/daemon/internal/model/storage"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/rs/zerolog/log"
	"net/url"
	"time"
)

type (
	StorageS3Repository struct {
		client *minio.Client
		bucket string
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
		deps.Config.Bucket,
	}
}

func newStorageS3(cfg *config.StorageS3Config) (*minio.Client, error) {
	return minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
}

func (r *StorageS3Repository) GetObjectInfo(ctx context.Context, objectName string) (*minio.ObjectInfo, error) {
	localCtx, _ := context.WithTimeout(ctx, 2*time.Second)
	objectInfo, err := r.client.StatObject(localCtx, r.bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return nil, storageModel.ErrStorageFileNotFound.WithError(err).WithMessage("Object not found").WithContext("objectName", objectName).Err()
		}

		return nil, storageModel.ErrStorage.WithError(err).WithMessage("Failed to get object info").Err()
	}

	return &objectInfo, nil
}

func (r *StorageS3Repository) GetObjectUploadLink(ctx context.Context, objectName string, expiredDuration time.Duration) (string, error) {
	objectURL, err := r.client.PresignedPutObject(ctx, r.bucket, objectName, expiredDuration)
	if err != nil {
		return "", storageModel.ErrStorage.WithError(err).WithMessage("Failed to get object presigned URL").Err()
	}

	return objectURL.String(), nil
}

func (r *StorageS3Repository) GetObjectDownloadLink(ctx context.Context, objectName string, expiredDuration time.Duration, reqParams url.Values) (string, error) {
	objectURL, err := r.client.PresignedGetObject(ctx, r.bucket, objectName, expiredDuration, reqParams)
	if err != nil {
		return "", storageModel.ErrStorage.WithError(err).WithMessage("Failed to get object presigned URL").Err()
	}

	return objectURL.String(), nil
}

func (r *StorageS3Repository) RemoveObject(ctx context.Context, objectName string) error {
	if err := r.client.RemoveObject(ctx, r.bucket, objectName, minio.RemoveObjectOptions{
		ForceDelete: true,
	}); err != nil {
		if minio.ToErrorResponse(err).Code == "NoSuchKey" {
			return storageModel.ErrStorageFileNotFound.WithError(err).WithMessage("Object not found").WithContext("objectName", objectName).Err()
		}

		return storageModel.ErrStorage.WithError(err).WithMessage("Failed to remove object").Err()
	}

	return nil
}
