package lablifecycleJob

import (
	"context"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/riverqueue/river"
)

type IUseCase interface{ ReconcilePendingLabLifecycles(context.Context) error }
type worker struct {
	river.WorkerDefaults[jobsModel.LabLifecycleArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.LabLifecycleArgs]) error {
	return w.uc.ReconcilePendingLabLifecycles(ctx)
}
