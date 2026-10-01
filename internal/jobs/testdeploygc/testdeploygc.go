package testdeploygcJob

import (
	"context"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/riverqueue/river"
)

type IUseCase interface{ CleanupExpiredTestDeploys(context.Context) error }
type worker struct {
	river.WorkerDefaults[jobsModel.TestDeployGCArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.TestDeployGCArgs]) error {
	return w.uc.CleanupExpiredTestDeploys(ctx)
}
func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
