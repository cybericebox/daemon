// Package eventscoringpopulationJob captures stable scoring populations once
// events reach their configured start time.
package eventscoringpopulationJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ CaptureDueScoringPopulations(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.EventScoringPopulationArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.EventScoringPopulationArgs]) error {
	return w.uc.CaptureDueScoringPopulations(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
