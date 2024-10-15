package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
	"time"
)

type (
	ExerciseUseCase struct {
		service IExerciseService
	}

	IExerciseService interface {
		IExerciseCategoryService

		GetExercises(ctx context.Context, search string) ([]*model.Exercise, error)
		GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error)
		CreateExercise(ctx context.Context, exercise model.Exercise) error
		UpdateExercise(ctx context.Context, exercise model.Exercise) error
		DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error

		GetUploadFileData(ctx context.Context, storageType string, expires ...time.Duration) (*model.UploadFileData, error)
		ConfirmFileUpload(ctx context.Context, fileID uuid.UUID) error
		GetDownloadFileLink(ctx context.Context, params model.DownloadFileParams) (string, error)
		DeleteFiles(ctx context.Context, files ...model.File) error
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

func (u *ExerciseUseCase) GetExercises(ctx context.Context, search string) ([]*model.Exercise, error) {
	exercises, err := u.service.GetExercises(ctx, search)
	if err != nil {
		return nil, model.ErrExercise.WithError(err).WithMessage("Failed to get exercises").Cause()
	}
	return exercises, nil
}

func (u *ExerciseUseCase) GetExercise(ctx context.Context, exerciseID uuid.UUID) (*model.Exercise, error) {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return nil, model.ErrExercise.WithError(err).WithMessage("Failed to get exercise").Cause()
	}
	return exercise, nil
}

func (u *ExerciseUseCase) CreateExercise(ctx context.Context, exercise model.Exercise) error {
	if err := u.service.CreateExercise(ctx, exercise); err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to create exercise").Cause()
	}
	// confirm file upload
	files := exercise.Data.Files
	for _, file := range files {
		if err := u.service.ConfirmFileUpload(ctx, file.ID); err != nil {
			return model.ErrExercise.WithError(err).WithMessage("Failed to confirm file upload").Cause()
		}
	}

	return nil
}

func (u *ExerciseUseCase) UpdateExercise(ctx context.Context, exercise model.Exercise) error {
	oldExercise, err := u.service.GetExercise(ctx, exercise.ID)
	if err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to get exercise").Cause()
	}
	// old attached files
	oldFiles := oldExercise.Data.Files

	// new attached files
	newFiles := exercise.Data.Files

	// compare files
	toAdd, toDelete := compareFileLists(oldFiles, newFiles)

	// confirm file upload
	for _, file := range toAdd {
		if err = u.service.ConfirmFileUpload(ctx, file.ID); err != nil {
			return model.ErrExercise.WithError(err).WithMessage("Failed to confirm file upload").Cause()
		}
	}

	// delete files that are not in new list
	if err = u.service.DeleteFiles(ctx, toDelete...); err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to delete files").Cause()
	}

	if err = u.service.UpdateExercise(ctx, exercise); err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to update exercise").Cause()
	}
	return nil
}

func (u *ExerciseUseCase) DeleteExercise(ctx context.Context, exerciseID uuid.UUID) error {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to get exercise").Cause()
	}

	// attached files
	files := exercise.Data.Files

	// delete exercise
	if err = u.service.DeleteExercise(ctx, exerciseID); err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to delete exercise").Cause()
	}
	_, toDelete := compareFileLists(files, nil)
	// delete all attached files
	if err = u.service.DeleteFiles(ctx, toDelete...); err != nil {
		return model.ErrExercise.WithError(err).WithMessage("Failed to delete files").Cause()
	}

	return nil
}

func (u *ExerciseUseCase) GetUploadFileData(ctx context.Context) (*model.UploadFileData, error) {
	uploadFileData, err := u.service.GetUploadFileData(ctx, model.TaskStorageType)
	if err != nil {
		return nil, model.ErrExercise.WithError(err).WithMessage("Failed to get upload file data").Cause()
	}
	return uploadFileData, nil
}

func (u *ExerciseUseCase) GetDownloadFileLink(ctx context.Context, exerciseID, fileID uuid.UUID, fileName string) (string, error) {
	exercise, err := u.service.GetExercise(ctx, exerciseID)
	if err != nil {
		return "", model.ErrExercise.WithError(err).WithMessage("Failed to get exercise").Cause()
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

	downloadFileLink, err := u.service.GetDownloadFileLink(ctx, model.DownloadFileParams{
		StorageType: model.TaskStorageType,
		FileID:      fileID,
		FileName:    fileName,
	})
	if err != nil {
		return "", model.ErrExercise.WithError(err).WithMessage("Failed to get download file link").Cause()
	}
	return downloadFileLink, nil
}

func compareFileLists(oldFiles, newFiles []model.ExerciseFile) (toAdd []model.ExerciseFile, toDelete []model.File) {
	oldFilesMap := make(map[uuid.UUID]model.ExerciseFile)
	for _, file := range oldFiles {
		oldFilesMap[file.ID] = file
	}

	newFilesMap := make(map[uuid.UUID]model.ExerciseFile)
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
			toDelete = append(toDelete, model.File{
				ID:          file.ID,
				StorageType: model.TaskStorageType,
			})
		}
	}

	return toAdd, toDelete
}
