// Package labcleanupJob wires withdrawn Laboratory teardown into River.
package labcleanupJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	CleanupWithdrawnLaboratories(context.Context) error
	CleanupQueuedLabGroups(context.Context) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.LabCleanupArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.LabCleanupArgs]) error {
	if err := w.uc.CleanupWithdrawnLaboratories(ctx); err != nil {
		return err
	}
	return w.uc.CleanupQueuedLabGroups(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
