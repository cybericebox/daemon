// Package exerciseexportJob exports tasks with device images outside any HTTP request.
package exerciseexportJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	RunArchiveExport(ctx context.Context, args jobsModel.ExerciseExportArgs) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.ExerciseExportArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

// Timeout: up to 10 GiB of images pulled and stored. A timed-out export still records itself failed.
func (w *worker) Timeout(*river.Job[jobsModel.ExerciseExportArgs]) time.Duration {
	return exerciseModel.ExportJobTimeout
}

func (w *worker) Work(ctx context.Context, job *river.Job[jobsModel.ExerciseExportArgs]) error {
	return w.uc.RunArchiveExport(ctx, job.Args)
}
