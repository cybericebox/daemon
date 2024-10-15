package storage

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/minio/minio-go/v7"
	"github.com/rs/zerolog/log"
	"net/url"
	"time"
)

type (
	StorageService struct {
		config     *config.StorageConfig
		repository IRepository
	}

	IRepository interface {
		CreateFile(ctx context.Context, params postgres.CreateFileParams) error
		DeleteFile(ctx context.Context, id []uuid.UUID) *postgres.DeleteFileBatchResults

		GetFiles(ctx context.Context) ([]postgres.File, error)

		PresignedPutObject(ctx context.Context, bucketName, objectName string, expires time.Duration) (*url.URL, error)
		PresignedGetObject(ctx context.Context, bucketName, objectName string, expires time.Duration, reqParams url.Values) (*url.URL, error)
		RemoveObject(ctx context.Context, bucketName string, objectName string, opts minio.RemoveObjectOptions) error
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

func (s *StorageService) GetTemporalUploadExpiredFiles(ctx context.Context, expiredDuration time.Duration) ([]model.File, error) {
	files, err := s.repository.GetFiles(ctx)
	if err != nil {
		return nil, model.ErrStorage.WithError(err).WithMessage("Failed to get files").Cause()
	}

	expiredFiles := make([]model.File, 0)
	for _, file := range files {
		if file.CreatedAt.Add(expiredDuration).Before(time.Now()) {
			expiredFiles = append(expiredFiles, model.File{
				ID:          file.ID,
				StorageType: file.StorageType,
				CreatedAt:   file.CreatedAt,
			})
		}
	}

	return expiredFiles, nil
}

func (s *StorageService) GetUploadFileData(ctx context.Context, storageType string, expires ...time.Duration) (*model.UploadFileData, error) {
	fileID := uuid.Must(uuid.NewV7())

	// create new file
	if err := s.repository.CreateFile(ctx, postgres.CreateFileParams{
		ID:          fileID,
		StorageType: storageType,
	}); err != nil {
		return nil, model.ErrStorage.WithError(err).WithMessage("Failed to create file").Cause()
	}

	expiresDuration := s.config.UploadExpiration
	// If expires is provided, use it
	if len(expires) > 0 {
		expiresDuration = expires[0]
	}

	// set object name as storageType/fileID
	objectName := fmt.Sprintf("%s/%s", storageType, fileID.String())

	uploadFileURL, err := s.repository.PresignedPutObject(ctx, s.config.BucketName, objectName, expiresDuration)
	if err != nil {
		return nil, model.ErrStorage.WithError(err).WithMessage("Failed to get presigned URL").Cause()
	}

	downloadFileURL, err := s.repository.PresignedGetObject(ctx, s.config.BucketName, objectName, expiresDuration, nil)
	if err != nil {
		return nil, model.ErrStorage.WithError(err).WithMessage("Failed to get presigned URL").Cause()
	}

	return &model.UploadFileData{
		FileID:      fileID,
		UploadURL:   uploadFileURL.String(),
		DownloadURL: downloadFileURL.String(),
	}, nil
}

func (s *StorageService) ConfirmFileUpload(ctx context.Context, fileID uuid.UUID) error {
	// delete files from database
	var dErr error
	batchResult := s.repository.DeleteFile(ctx, []uuid.UUID{fileID})
	defer func() {
		if err := batchResult.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close batch result")
		}
	}()

	batchResult.Exec(func(i int, affected int64, err error) {
		if err != nil {
			dErr = model.ErrStorage.WithError(err).WithMessage("Failed to delete file").WithContext("fileID", fileID).Cause()
		}

		if affected == 0 {
			dErr = model.ErrStorageTemporalFileNotFound.WithContext("fileID", fileID).Cause()
		}
	})

	if dErr != nil {
		return dErr
	}

	return nil
}

func (s *StorageService) GetDownloadFileLink(ctx context.Context, params model.DownloadFileParams) (string, error) {
	expiresDuration := s.config.DownloadExpiration
	// If expires is provided, use it
	if params.Expires > 0 {
		expiresDuration = params.Expires
	}

	// set object name as storageType/fileID
	objectName := fmt.Sprintf("%s/%s", params.StorageType, params.FileID.String())

	// set response-content-disposition to attachment; filename="file.Name"
	reqParams := url.Values{}
	reqParams.Set("response-content-disposition", fmt.Sprintf("attachment; filename=\"%s\"", params.FileName))

	fileURL, err := s.repository.PresignedGetObject(ctx, s.config.BucketName, objectName, expiresDuration, reqParams)
	if err != nil {
		return "", model.ErrStorage.WithError(err).WithMessage("Failed to get presigned URL").Cause()
	}

	return fileURL.String(), nil
}

func (s *StorageService) DeleteFiles(ctx context.Context, files ...model.File) error {
	var errs error

	fileIDs := make([]uuid.UUID, 0, len(files))
	for _, file := range files {
		// set object name as storageType/fileID
		objectName := fmt.Sprintf("%s/%s", file.StorageType, file.ID.String())

		// delete object from storage
		if err := s.repository.RemoveObject(ctx, s.config.BucketName, objectName, minio.RemoveObjectOptions{
			ForceDelete: true,
		}); err != nil {
			errs = multierror.Append(errs, model.ErrStorage.WithError(err).WithMessage("Failed to delete object").Cause())
			continue
		}
		fileIDs = append(fileIDs, file.ID)
	}

	// delete files from database
	batchResult := s.repository.DeleteFile(ctx, fileIDs)
	defer func() {
		if err := batchResult.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close batch result")
		}
	}()

	batchResult.Exec(func(i int, affected int64, err error) {
		if err != nil {
			errs = multierror.Append(errs, model.ErrStorage.WithError(err).WithMessage("Failed to delete file").WithContext("fileID", fileIDs[i]).Cause())
		}
	})

	if errs != nil {
		return model.ErrStorage.WithError(errs).WithMessage("Failed to delete files").Cause()
	}

	return nil
}
