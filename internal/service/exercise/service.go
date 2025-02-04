package exerciseService

import (
	"github.com/cybericebox/daemon/internal/service/exercise/category"
	"github.com/cybericebox/daemon/internal/service/exercise/exercise"
)

type (
	ExerciseService struct {
		*exerciseService.ExerciseService
		*categoryService.CategoryService
	}

	IRepository interface {
		categoryService.IRepository
		exerciseService.IRepository
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *ExerciseService {
	return &ExerciseService{
		exerciseService.NewService(exerciseService.Dependencies{
			Repository: deps.Repository,
		}),
		categoryService.NewService(categoryService.Dependencies{
			Repository: deps.Repository,
		}),
	}
}
