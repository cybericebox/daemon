// Package testdeployexpiryJob wires the end of an exercise test lab's lease into River.
package testdeployexpiryJob

import (
	"context"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	EndExpiredTestDeploy(ctx context.Context, ownerID, deployID uuid.UUID) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.TestDeployExpiryArgs]

	uc IUseCase
}

func (w *worker) Work(ctx context.Context, job *river.Job[jobsModel.TestDeployExpiryArgs]) error {
	return w.uc.EndExpiredTestDeploy(ctx, job.Args.OwnerID, job.Args.DeployID)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
