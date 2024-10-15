package platform

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/rs/zerolog/log"
	"time"
)

type (
	IPlatformHooksService interface {
		GetTemporalUploadExpiredFiles(ctx context.Context, expiredDuration time.Duration) ([]model.File, error)
		DeleteFiles(ctx context.Context, files ...model.File) error

		DeleteExpiredTemporalCodes(ctx context.Context) error
	}
)

func (u *PlatformUseCase) CleanTemporalCodes(ctx context.Context) error {
	if err := u.service.DeleteExpiredTemporalCodes(ctx); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to clean temporal codes").Cause()
	}

	return nil
}

func (u *PlatformUseCase) CleanTemporalUploadFiles(ctx context.Context) error {
	expiredDuration := 24 * time.Hour
	files, err := u.service.GetTemporalUploadExpiredFiles(ctx, expiredDuration)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get temporal upload expired files").Cause()
	}

	if err = u.service.DeleteFiles(ctx, files...); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete temporal upload expired files").Cause()
	}

	return nil
}

func (u *PlatformUseCase) InitPlatformHooks(ctx context.Context) {
	// clean temporal codes
	u.worker.AddTask(worker.Task{
		Do: func() {
			if err := u.CleanTemporalCodes(ctx); err != nil {
				log.Error().Err(err).Msg("Failed to clean temporal codes")
			}
		},
		RepeatDuration: 7 * 24 * time.Hour, // every week
		TimeToDo:       time.Now(),
		CheckIfNeedToDo: func() (need bool, nextTimeToDo *time.Time) {
			return true, nil
		},
	})

	// clean temporal upload files
	u.worker.AddTask(worker.Task{
		Do: func() {
			if err := u.CleanTemporalUploadFiles(ctx); err != nil {
				log.Error().Err(err).Msg("Failed to clean temporal upload files")
			}
		},
		RepeatDuration: 7 * 24 * time.Hour, // every week
		TimeToDo:       time.Now(),
		CheckIfNeedToDo: func() (need bool, nextTimeToDo *time.Time) {
			return true, nil
		},
	})

}
