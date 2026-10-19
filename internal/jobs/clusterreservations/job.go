// Package clusterreservationsJob wires the periodic reconcile of the cluster reservations into River.
package clusterreservationsJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ ReconcileClusterReservations(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.ClusterReservationsArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

// Timeout covers one pass over every agent.
func (w *worker) Timeout(*river.Job[jobsModel.ClusterReservationsArgs]) time.Duration {
	return 2 * time.Minute
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.ClusterReservationsArgs]) error {
	return w.uc.ReconcileClusterReservations(ctx)
}
