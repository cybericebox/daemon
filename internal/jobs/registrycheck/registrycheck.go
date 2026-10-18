// Package registrycheckJob wires the periodic check of the image registry credentials (platform push and pull
// tokens, organizer secrets of live events) into River.
package registrycheckJob

import (
	"context"

	"github.com/riverqueue/river"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type IUseCase interface {
	RunRegistryChecks(ctx context.Context) error
}

type worker struct {
	river.WorkerDefaults[jobsModel.RegistryCheckArgs]
	uc IUseCase
}

func (w *worker) Work(ctx context.Context, _ *river.Job[jobsModel.RegistryCheckArgs]) error {
	return w.uc.RunRegistryChecks(ctx)
}

func NewWorker(uc IUseCase) *worker { return &worker{uc: uc} }
