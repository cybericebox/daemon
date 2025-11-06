package platform

import (
	"context"
	"time"

	"github.com/cybericebox/lib/pkg/worker"

	"github.com/cybericebox/daemon/internal/model"
	storageModel "github.com/cybericebox/daemon/internal/model/storage"
)

type (
	IPlatformHooksService interface {
		GetTemporalUploadExpiredFiles(ctx context.Context, expiredDuration time.Duration) ([]storageModel.File, error)
		DeleteFiles(ctx context.Context, files ...storageModel.File) error

		DeleteExpiredTemporalCodes(ctx context.Context) error
	}
)

func (u *PlatformUseCase) CleanTemporalCodes(ctx context.Context) error {
	if err := u.service.DeleteExpiredTemporalCodes(ctx); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to clean temporal codes").Err()
	}

	return nil
}

func (u *PlatformUseCase) CleanTemporalUploadFiles(ctx context.Context) error {
	expiredDuration := 24 * time.Hour
	files, err := u.service.GetTemporalUploadExpiredFiles(ctx, expiredDuration)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get temporal upload expired files").Err()
	}

	if err = u.service.DeleteFiles(ctx, files...); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete temporal upload expired files").Err()
	}

	return nil
}

func (u *PlatformUseCase) InitPlatformHooks(ctx context.Context) {
	// clean temporal codes
	u.worker.AddTask(
		worker.NewTask().
			WithKey("clean_temporal_codes").
			WithDo(
				func(ctx context.Context) error {
					if err := u.CleanTemporalCodes(ctx); err != nil {
						return model.ErrPlatform.WithError(err).WithMessage("Failed to clean temporal codes").Err()
					}
					return nil
				},
			).WithRepeatDuration(7 * 24 * time.Hour).
			Create(),
	)

	// clean temporal upload files
	u.worker.AddTask(
		worker.NewTask().
			WithKey("clean_temporal_upload_files").
			WithDo(
				func(ctx context.Context) error {
					if err := u.CleanTemporalUploadFiles(ctx); err != nil {
						return model.ErrPlatform.WithError(err).WithMessage("Failed to clean temporal upload files").Err()
					}

					return nil
				},
			).
			WithRepeatDuration(7 * 24 * time.Hour).
			Create(),
	)

}
