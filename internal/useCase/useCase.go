package useCase

import (
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/internal/useCase/storage"
	"github.com/cybericebox/daemon/internal/useCase/user"
	"github.com/cybericebox/daemon/pkg/worker"
)

type (
	UseCase struct {
		*storage.StorageUseCase
		*auth.AuthUseCase
		*user.UserUseCase
		*exercise.ExerciseUseCase
		*event.EventUseCase
	}

	IService interface {
		storage.IStorageService
		auth.IAuthService
		user.IUserService
		exercise.IExerciseService
		event.IEventService
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
		StorageUseCase: storage.NewUseCase(storage.Dependencies{
			Service: deps.Service,
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
