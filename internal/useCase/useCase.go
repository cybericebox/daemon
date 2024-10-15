package useCase

import (
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/internal/useCase/platform"
	"github.com/cybericebox/daemon/internal/useCase/user"
	"github.com/cybericebox/daemon/pkg/worker"
)

type (
	UseCase struct {
		*auth.AuthUseCase
		*user.UserUseCase
		*exercise.ExerciseUseCase
		*event.EventUseCase
		*platform.PlatformUseCase
	}

	IService interface {
		auth.IAuthService
		user.IUserService
		exercise.IExerciseService
		event.IEventService
		platform.IPlatformService
	}

	Worker interface {
		AddTask(task worker.Task)
	}

	Dependencies struct {
		Service IService
		Worker  Worker
	}
)

func NewUseCase(deps Dependencies) *UseCase {
	return &UseCase{
		PlatformUseCase: platform.NewUseCase(platform.Dependencies{
			Service: deps.Service,
			Worker:  deps.Worker,
		}),
		AuthUseCase: auth.NewUseCase(auth.Dependencies{
			Service: deps.Service,
		}),
		UserUseCase: user.NewUseCase(user.Dependencies{
			Service: deps.Service,
		}),
		ExerciseUseCase: exercise.NewUseCase(exercise.Dependencies{
			Service: deps.Service,
		}),
		EventUseCase: event.NewUseCase(event.Dependencies{
			Service: deps.Service,
			Worker:  deps.Worker,
		}),
	}
}
