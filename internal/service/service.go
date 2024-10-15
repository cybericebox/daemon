package service

import (
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/service/email"
	"github.com/cybericebox/daemon/internal/service/event"
	"github.com/cybericebox/daemon/internal/service/exercise"
	"github.com/cybericebox/daemon/internal/service/laboratory"
	"github.com/cybericebox/daemon/internal/service/oauth"
	"github.com/cybericebox/daemon/internal/service/storage"
	"github.com/cybericebox/daemon/internal/service/temporalCode"
	"github.com/cybericebox/daemon/internal/service/token"
	"github.com/cybericebox/daemon/internal/service/user"
	"github.com/cybericebox/daemon/pkg/password"
)

type (
	Service struct {
		*oauth.OAuthService
		*password.Manager
		*storage.StorageService
		*temporalCode.TemporalCodeService
		*email.EmailService
		*token.TokenService
		*user.UserService
		*event.EventService
		*exercise.ExerciseService
		*laboratory.LaboratoryService
	}

	IRepository interface {
		storage.IRepository
		temporalCode.IRepository
		email.IRepository
		user.IRepository
		event.IRepository
		exercise.IRepository
		laboratory.IRepository
	}

	Dependencies struct {
		Config     *config.ServiceConfig
		Repository IRepository
	}
)

func NewService(deps Dependencies) *Service {
	return &Service{
		OAuthService: oauth.NewOAuthService(oauth.Dependencies{Config: &deps.Config.OAuth}),
		Manager: password.NewHashManager(password.Dependencies{
			Cost:               deps.Config.Password.HashCost,
			PasswordComplexity: password.PasswordComplexityConfig(deps.Config.Password.PasswordComplexity),
		}),
		StorageService: storage.NewStorageService(storage.Dependencies{Repository: deps.Repository, Config: &deps.Config.Storage}),
		TemporalCodeService: temporalCode.NewTemporalCodeService(temporalCode.Dependencies{
			Repository: deps.Repository,
			Config:     &deps.Config.TemporalCode,
		}),
		EmailService: email.NewEmailService(email.Dependencies{Repository: deps.Repository}),
		TokenService: token.NewTokenService(token.Dependencies{
			Config: &deps.Config.JWT,
		}),
		UserService: user.NewUserService(user.Dependencies{Repository: deps.Repository}),
		EventService: event.NewEventService(event.Dependencies{
			Repository: deps.Repository,
		}),
		ExerciseService:   exercise.NewExerciseService(exercise.Dependencies{Repository: deps.Repository}),
		LaboratoryService: laboratory.NewLaboratoryService(laboratory.Dependencies{Repository: deps.Repository}),
	}
}
