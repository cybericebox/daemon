// Package labgroupsweepJob wires the orphan lab group sweep into River.
package labgroupsweepJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	SweepOrphanLabGroups(context.Context) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.LabGroupSweepArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.LabGroupSweepArgs]) error {
	return w.uc.SweepOrphanLabGroups(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
