// Package idempotencygcJob wires short-lived request replay cleanup into River.
package idempotencygcJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ CleanupExpiredRequestIdempotency(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.IdempotencyGCArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.IdempotencyGCArgs]) error {
	return w.uc.CleanupExpiredRequestIdempotency(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
