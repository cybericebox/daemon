// Package resourcecalendarJob wires the resource calendar's readiness check into River.
package resourcecalendarJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ ReconcileResourceCalendar(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.ResourceCalendarArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

// Timeout covers a pass over every upcoming reservation.
func (w *worker) Timeout(*river.Job[jobsModel.ResourceCalendarArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.ResourceCalendarArgs]) error {
	return w.uc.ReconcileResourceCalendar(ctx)
}
