// Package accountinactivityJob wires the inactive-account warning and
// deletion into River.
package accountinactivityJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ EnforceAccountInactivity(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.AccountInactivityArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.AccountInactivityArgs]) error {
	return w.uc.EnforceAccountInactivity(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
