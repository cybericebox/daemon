// Package eventstandsJob wires the team stand engine into River.
package eventstandsJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ ReconcileEventStands(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.EventStandsArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

// Timeout covers a pass that deploys many Labs; each agent deploy waits for the
// group namespace for a bounded time.
func (w *worker) Timeout(*river.Job[jobsModel.EventStandsArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.EventStandsArgs]) error {
	return w.uc.ReconcileEventStands(ctx)
}
