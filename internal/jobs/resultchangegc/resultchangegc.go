// Package resultchangegcJob wires short-lived live-result cleanup into River.
package resultchangegcJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ CleanupExpiredResultChanges(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.ResultChangeGCArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.ResultChangeGCArgs]) error {
	return w.uc.CleanupExpiredResultChanges(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
