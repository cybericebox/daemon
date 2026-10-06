// Package dataretentionJob wires the data retention purge into River.
package dataretentionJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ EnforceDataRetention(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.DataRetentionArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.DataRetentionArgs]) error {
	return w.uc.EnforceDataRetention(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
