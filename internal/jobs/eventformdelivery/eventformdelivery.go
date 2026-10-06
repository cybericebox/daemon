// Package eventformdeliveryJob materializes due timed Event-form assignments.
package eventformdeliveryJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ MaterializeDueFormDeliveries(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.EventFormDeliveryArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.EventFormDeliveryArgs]) error {
	return w.uc.MaterializeDueFormDeliveries(ctx)
}
