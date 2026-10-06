// Package broadcastsendJob wires the custom broadcast sender into River.
package broadcastsendJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	ProcessBroadcast(ctx context.Context, args jobsModel.BroadcastSendArgs) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.BroadcastSendArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

func (w *worker) Work(ctx context.Context, job *river.Job[jobsModel.BroadcastSendArgs]) error {
	return w.uc.ProcessBroadcast(ctx, job.Args)
}
