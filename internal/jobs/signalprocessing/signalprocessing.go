// Package signalprocessingJob wires the durable signal processor into River.
package signalprocessingJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ ProcessPending(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.SignalProcessingArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.SignalProcessingArgs]) error {
	return w.uc.ProcessPending(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
