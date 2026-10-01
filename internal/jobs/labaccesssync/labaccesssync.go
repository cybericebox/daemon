// Package labaccesssyncJob wires durable Laboratory ACL reconciliation into River.
package labaccesssyncJob

import (
	"context"

	"github.com/riverqueue/river"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ ReconcilePendingLabAccess(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.LabAccessSyncArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.LabAccessSyncArgs]) error {
	err := w.uc.ReconcilePendingLabAccess(ctx)
	// The group or client is still being deleted: not a failure. The revision
	// stays dirty; run again once the deletion is likely finished.
	if terminating, ok := infraModel.AsTerminating(err); ok {
		return river.JobSnooze(terminating.RetryAfter)
	}
	return err
}
