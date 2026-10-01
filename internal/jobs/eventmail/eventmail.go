// Package eventmailJob wires the Event mail notices (start reminder, event
// finished, expired invitations) into River.
package eventmailJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface{ RunEventMailNotices(context.Context) error }

type worker struct {
	river.WorkerDefaults[jobsModel.EventMailArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.EventMailArgs]) error {
	return w.uc.RunEventMailNotices(ctx)
}
