// Package exerciseimportJob imports a task archive with device images outside any HTTP request.
package exerciseimportJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	RunArchiveImport(ctx context.Context, args jobsModel.ExerciseImportArgs) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.ExerciseImportArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

// Timeout: up to 10 GiB of images pushed at a slow uplink. A timed-out import still records itself failed.
func (w *worker) Timeout(*river.Job[jobsModel.ExerciseImportArgs]) time.Duration {
	return exerciseModel.ImportJobTimeout
}

func (w *worker) Work(ctx context.Context, job *river.Job[jobsModel.ExerciseImportArgs]) error {
	return w.uc.RunArchiveImport(ctx, job.Args)
}
