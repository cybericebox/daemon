package storageService

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/storage"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/minio/minio-go/v7"
	"github.com/rs/zerolog/log"
	"net/url"
	"strings"
	"time"
)

type (
	StorageService struct {
		config     *config.StorageConfig
		repository IRepository
	}

	IRepository interface {
		// postgres methods
		GetFiles(ctx context.Context) ([]postgres.File, error)
		CreateFile(ctx context.Context, params postgres.CreateFileParams) error
		DeleteFile(ctx context.Context, id []uuid.UUID) *postgres.DeleteFileBatchResults

		// minio methods

		GetObjectInfo(ctx context.Context, objectName string) (*minio.ObjectInfo, error)
		GetObjectUploadLink(ctx context.Context, objectName string, expiredDuration time.Duration) (string, error)
		GetObjectDownloadLink(ctx context.Context, objectName string, expiredDuration time.Duration, reqParams url.Values) (string, error)
		RemoveObject(ctx context.Context, objectName string) error
	}

	Dependencies struct {
		Config     *config.StorageConfig
		Repository IRepository
	}
)

func NewService(deps Dependencies) *StorageService {
	return &StorageService{
		config:     deps.Config,
		repository: deps.Repository,
	}
}

func (s *StorageService) GetUploadFileData(ctx context.Context, params storageModel.UploadFileParams) (*storageModel.UploadFileData, error) {
	fileID := uuid.Must(uuid.NewV7())

	// create record for temporal file
	if err := s.repository.CreateFile(ctx, postgres.CreateFileParams{
		ID:          fileID,
		StorageType: params.StorageType,
	}); err != nil {
		return nil, storageModel.ErrStorage.WithError(err).WithMessage("Failed to create file").Err()
	}

	expiresDuration := s.config.UploadExpiration
	// If expires is provided, use it
	if params.Expires > 0 {
		expiresDuration = params.Expires
	}

	// set object name as storageType/fileID
	objectName := fmt.Sprintf("%s/%s", params.StorageType, fileID.String())

	uploadFileLink, err := s.repository.GetObjectUploadLink(ctx, objectName, expiresDuration)
	if err != nil {
		return nil, storageModel.ErrStorage.WithError(err).WithMessage("Failed to get presigned URL").Err()
	}

	downloadFileLink, err := s.repository.GetObjectDownloadLink(ctx, objectName, expiresDuration, nil)
	if err != nil {
		return nil, storageModel.ErrStorage.WithError(err).WithMessage("Failed to get presigned URL").Err()
	}

	return &storageModel.UploadFileData{
		FileID:      fileID,
		UploadURL:   uploadFileLink,
		DownloadURL: downloadFileLink,
	}, nil
}

func (s *StorageService) ConfirmUploadFiles(ctx context.Context, fileIDs ...uuid.UUID) error {
	// delete temporal files records from database
	var dErr error
	batchResult := s.repository.DeleteFile(ctx, fileIDs)
	defer func() {
		if err := batchResult.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close batch result")
		}
	}()

	batchResult.Exec(func(i int, affected int64, err error) {
		if err != nil {
			dErr = storageModel.ErrStorage.WithError(err).WithMessage("Failed to delete file").WithContext("fileID", fileIDs[i]).Err()
		}

		if affected == 0 {
			dErr = storageModel.ErrStorageTemporalFileNotFound.WithContext("fileID", fileIDs[i]).Err()
		}
	})

	if dErr != nil {
		return dErr
	}

	return nil
}

func (s *StorageService) GetDownloadFileURL(ctx context.Context, params storageModel.DownloadFileParams) (storageModel.DownloadFileURL, error) {
	expiresDuration := s.config.DownloadExpiration
	// If expires is provided, use it
	if params.Expires > 0 {
		expiresDuration = params.Expires
	}

	// set object name as storageType/fileID
	objectName := fmt.Sprintf("%s/%s", params.StorageType, params.FileID.String())

	// set response-content-disposition to attachment; filename="file.Name"
	reqParams := url.Values{}

	// if filename is not provided, use fileID and object content type as filename
	if params.FileName == "" || !params.WithExtension {
		contentType := ""
		objectInfo, err := s.repository.GetObjectInfo(ctx, objectName)
		if err != nil {
			if errors.Is(err, storageModel.ErrStorageFileNotFound.Err()) {
				return "", storageModel.ErrStorage.WithError(err).WithMessage("Failed to get object info").Err()
			}
			log.Error().Err(err).Msg("Failed to get object info")
		}

		if params.FileName == "" {
			params.FileName = params.FileID.String()
		}

		if objectInfo != nil {
			contentType = objectInfo.ContentType
		}

		params.FileName = updateNameByContentType(contentType, params.FileName)
	}

	reqParams.Set("response-content-disposition", fmt.Sprintf("attachment; filename=\"%s\"", params.FileName))

	fileLink, err := s.repository.GetObjectDownloadLink(ctx, objectName, expiresDuration, reqParams)
	if err != nil {
		return "", storageModel.ErrStorage.WithError(err).WithMessage("Failed to get presigned URL").Err()
	}

	return storageModel.DownloadFileURL(fileLink), nil
}

func (s *StorageService) DeleteFiles(ctx context.Context, files ...storageModel.File) error {
	var errs error

	fileIDs := make([]uuid.UUID, 0, len(files))
	for _, file := range files {
		// set object name as storageType/fileID
		objectName := fmt.Sprintf("%s/%s", file.StorageType, file.ID.String())

		// delete object from storage
		if err := s.repository.RemoveObject(ctx, objectName); err != nil {
			if !errors.Is(err, storageModel.ErrStorageFileNotFound.Err()) {
				errs = multierror.Append(errs, storageModel.ErrStorage.WithError(err).WithMessage("Failed to delete object").Err())
			}
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
			errs = multierror.Append(errs, storageModel.ErrStorage.WithError(err).WithMessage("Failed to delete file").WithContext("fileID", fileIDs[i]).Err())
		}
	})

	if errs != nil {
		return storageModel.ErrStorage.WithError(errs).WithMessage("Failed to delete files").Err()
	}

	return nil
}

func (s *StorageService) GetTemporalUploadExpiredFiles(ctx context.Context, expiredDuration time.Duration) ([]storageModel.File, error) {
	files, err := s.repository.GetFiles(ctx)
	if err != nil {
		return nil, storageModel.ErrStorage.WithError(err).WithMessage("Failed to get files").Err()
	}

	expiredFiles := make([]storageModel.File, 0)
	for _, file := range files {
		if file.CreatedAt.Add(expiredDuration).Before(time.Now()) {
			expiredFiles = append(expiredFiles, storageModel.File{
				ID:          file.ID,
				StorageType: file.StorageType,
				CreatedAt:   file.CreatedAt,
			})
		}
	}

	return expiredFiles, nil
}

func updateNameByContentType(contentType, fileName string) string {
	if !strings.HasPrefix(contentType, "image/") {
		return fileName
	}

	rest := strings.Split(contentType, "/")[1]
	switch rest {
	case "vnd.microsoft.icon":
		return fileName + ".ico"
	case "svg+xml":
		return fileName + ".svg"
	case "jpeg", "jpg", "png", "gif", "bmp", "webp", "apng", "avif":
		return fileName + "." + rest
	default:
		return fileName
	}

}
