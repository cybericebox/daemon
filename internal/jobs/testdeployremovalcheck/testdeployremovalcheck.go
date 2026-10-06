// Package testdeployremovalcheckJob wires the short follow-up check of a test lab that is being removed into River.
package testdeployremovalcheckJob

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	CheckTestDeployRemoved(ctx context.Context, ownerID, deployID uuid.UUID, attempt int) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.TestDeployRemovalCheckArgs]

	uc IUseCase
}

func (w *worker) Work(ctx context.Context, job *river.Job[jobsModel.TestDeployRemovalCheckArgs]) error {
	return w.uc.CheckTestDeployRemoved(ctx, job.Args.OwnerID, job.Args.DeployID, job.Args.Attempt)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
