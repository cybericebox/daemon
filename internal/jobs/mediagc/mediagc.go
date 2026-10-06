// Package mediagcJob wires the periodic media GC into River.
package mediagcJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

// mediagcWorker is the consumer: it has no payload to decode, it just runs
// the use-case's GC pass.

type IUseCase interface {
	CleanupOrphanFiles(ctx context.Context) error
}
type mediagcWorker struct {
	river.WorkerDefaults[jobsModel.MediaGCArgs]
	uc IUseCase
}

func (w *mediagcWorker) Work(ctx context.Context, _ *river.Job[jobsModel.MediaGCArgs]) error {
	return w.uc.CleanupOrphanFiles(ctx)
}

// NewWorker builds this job's River worker from its narrow use-case port. The
// central worker registry imports this constructor and adds it to the bundle.
func NewWorker(uc IUseCase) *mediagcWorker {
	return &mediagcWorker{uc: uc}
}
