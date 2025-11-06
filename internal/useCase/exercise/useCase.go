package exercise

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	storageModel "github.com/cybericebox/daemon/internal/model/storage"
)

type (
	ExerciseUseCase struct {
		service IExerciseService
	}

	IExerciseService interface {
		IExerciseCategoryService

		GetExercises(ctx context.Context, search string, page, pageSize int) ([]*exerciseModel.Exercise, error)
		GetExercise(ctx context.Context, exerciseID uuid.UUID) (*exerciseModel.Exercise, error)
		CreateExercise(ctx context.Context, exercise exerciseModel.Exercise) error
		UpdateExercise(ctx context.Context, exercise exerciseModel.Exercise) error
		DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error

		ConfirmUploadFiles(ctx context.Context, fileIDs ...uuid.UUID) error
		GetUploadFileData(ctx context.Context, params storageModel.UploadFileParams) (
			*storageModel.UploadFileData,
			error,
		)
		GetDownloadFileURL(
			ctx context.Context,
			params storageModel.DownloadFileParams,
		) (storageModel.DownloadFileURL, error)
		DeleteFiles(ctx context.Context, files ...storageModel.File) error
	}

	Dependencies struct {
		Service IExerciseService
	}
)

func NewUseCase(deps Dependencies) *ExerciseUseCase {
	return &ExerciseUseCase{
		service: deps.Service,
	}
}

func (u *ExerciseUseCase) GetExercises(
	ctx context.Context,
	search string,
	page, pageSize int,
) ([]*exerciseModel.Exercise, error) {
	exercises, err := u.service.GetExercises(ctx, search, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercises").Err()
	}
	return exercises, nil
}

func (u *ExerciseUseCase) GetExercise(ctx context.Context, exerciseID uuid.UUID) (*exerciseModel.Exercise, error) {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	return exercise, nil
}

func (u *ExerciseUseCase) CreateExercise(ctx context.Context, exercise exerciseModel.Exercise) error {
	if err := u.service.CreateExercise(ctx, exercise); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create exercise").Err()
	}
	// confirm file upload
	files := exercise.Data.Files
	for _, file := range files {
		if err := u.service.ConfirmUploadFiles(ctx, file.ID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to confirm file upload").Err()
		}
	}

	return nil
}

func (u *ExerciseUseCase) UpdateExercise(ctx context.Context, exercise exerciseModel.Exercise) error {
	oldExercise, err := u.service.GetExercise(ctx, exercise.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}
	// old attached files
	oldFiles := oldExercise.Data.Files

	// new attached files
	newFiles := exercise.Data.Files

	// compare files
	toAdd, toDelete := compareFileLists(oldFiles, newFiles)

	// confirm file upload
	for _, file := range toAdd {
		if err = u.service.ConfirmUploadFiles(ctx, file.ID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to confirm file upload").Err()
		}
	}

	// delete files that are not in new list
	if err = u.service.DeleteFiles(ctx, toDelete...); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete files").Err()
	}

	if err = u.service.UpdateExercise(ctx, exercise); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update exercise").Err()
	}
	return nil
}

func (u *ExerciseUseCase) DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}

	// attached files
	files := exercise.Data.Files

	// delete exercise
	if err = u.service.DeleteExercise(ctx, exerciseID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete exercise").Err()
	}
	_, toDelete := compareFileLists(files, nil)
	// delete all attached files
	if err = u.service.DeleteFiles(ctx, toDelete...); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete files").Err()
	}

	return nil
}

func (u *ExerciseUseCase) GetUploadFileData(ctx context.Context) (*storageModel.UploadFileData, error) {
	uploadFileData, err := u.service.GetUploadFileData(
		ctx, storageModel.UploadFileParams{
			StorageType: storageModel.TaskStorageType,
		},
	)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get upload file data").Err()
	}
	return uploadFileData, nil
}

func (u *ExerciseUseCase) GetDownloadFileLink(
	ctx context.Context,
	exerciseID, fileID uuid.UUID,
	fileName string,
) (string, error) {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get exercise").Err()
	}

	// find file
	if fileName == "" {
		fileName = fileID.String()
	}
	for _, file := range exercise.Data.Files {
		if file.ID == fileID {
			fileName = file.Name
			break
		}
	}

	downloadFileLink, err := u.service.GetDownloadFileURL(
		ctx, storageModel.DownloadFileParams{
			StorageType: storageModel.TaskStorageType,
			FileID:      fileID,
			FileName:    fileName,
		},
	)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get download file link").Err()
	}
	return string(downloadFileLink), nil
}

func compareFileLists(oldFiles, newFiles []exerciseModel.ExerciseFile) (
	toAdd []exerciseModel.ExerciseFile,
	toDelete []storageModel.File,
) {
	oldFilesMap := make(map[uuid.UUID]exerciseModel.ExerciseFile)
	for _, file := range oldFiles {
		oldFilesMap[file.ID] = file
	}

	newFilesMap := make(map[uuid.UUID]exerciseModel.ExerciseFile)
	for _, file := range newFiles {
		newFilesMap[file.ID] = file
	}

	for id, file := range newFilesMap {
		if _, ok := oldFilesMap[id]; !ok {
			toAdd = append(toAdd, file)
		}
	}

	for id, file := range oldFilesMap {
		if _, ok := newFilesMap[id]; !ok {
			toDelete = append(
				toDelete, storageModel.File{
					ID:          file.ID,
					StorageType: storageModel.TaskStorageType,
				},
			)
		}
	}

	return toAdd, toDelete
}
