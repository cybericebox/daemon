// Package agentmaintenanceJob wires the infrastructure agent upkeep into River.
package agentmaintenanceJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ MaintainAgents(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.AgentMaintenanceArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

// Timeout covers a pass over several agents, each call to an agent being bounded by its own deadline.
func (w *worker) Timeout(*river.Job[jobsModel.AgentMaintenanceArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.AgentMaintenanceArgs]) error {
	return w.uc.MaintainAgents(ctx)
}
