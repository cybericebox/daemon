package platform

import "github.com/cybericebox/lib/pkg/worker"

type (
	PlatformUseCase struct {
		service IPlatformService
		worker  worker.Worker
	}

	IPlatformService interface {
		IPlatformHooksService
	}

	Dependencies struct {
		Service IPlatformService
		Worker  worker.Worker
	}
)

func NewUseCase(deps Dependencies) *PlatformUseCase {
	return &PlatformUseCase{
		service: deps.Service,
		worker:  deps.Worker,
	}

}
