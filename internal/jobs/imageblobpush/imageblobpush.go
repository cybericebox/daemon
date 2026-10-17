// Package imageblobpushJob streams one chunked image-blob upload into the platform registry, outside any HTTP
// request.
package imageblobpushJob

import (
	"context"
	"time"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	PushImageBlob(ctx context.Context, args jobsModel.ImageBlobPushArgs) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.ImageBlobPushArgs]
	uc IUseCase
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }

// Timeout: a 10 GiB blob at a slow uplink; the push state counts a push silent for longer as failed anyway.
func (w *worker) Timeout(*river.Job[jobsModel.ImageBlobPushArgs]) time.Duration { return 3 * time.Hour }

func (w *worker) Work(ctx context.Context, job *river.Job[jobsModel.ImageBlobPushArgs]) error {
	return w.uc.PushImageBlob(ctx, job.Args)
}
