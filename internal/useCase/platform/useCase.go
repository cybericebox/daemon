package platform

import "github.com/cybericebox/daemon/pkg/worker"

type (
	PlatformUseCase struct {
		service IPlatformService
		worker  Worker
	}

	IPlatformService interface {
		IPlatformHooksService
	}

	Worker interface {
		AddTask(task worker.Task)
	}

	Dependencies struct {
		Service IPlatformService
		Worker  Worker
	}
)

func NewUseCase(deps Dependencies) *PlatformUseCase {
	return &PlatformUseCase{
		service: deps.Service,
		worker:  deps.Worker,
	}

}
