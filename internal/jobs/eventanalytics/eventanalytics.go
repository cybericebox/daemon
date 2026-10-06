// Package eventanalyticsJob wires the event analytics rollup pass into River.
package eventanalyticsJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ RefreshEventAnalytics(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.EventAnalyticsArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.EventAnalyticsArgs]) error {
	return w.uc.RefreshEventAnalytics(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
