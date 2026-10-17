// Package imageregistrygcJob wires the periodic removal of unused platform registry tags into River.
package imageregistrygcJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	CollectUnusedTags(ctx context.Context, now time.Time) (int, error)
}

type worker struct {
	river.WorkerDefaults[jobsModel.ImageRegistryGCArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.ImageRegistryGCArgs]) error {
	_, err := w.uc.CollectUnusedTags(ctx, time.Now().UTC())
	return err
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
