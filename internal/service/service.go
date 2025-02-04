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
	"github.com/cybericebox/lib/pkg/password"
)

type (
	Service struct {
		*oauthService.OAuthService
		*password.Manager
		*storageService.StorageService
		*temporalCodeService.TemporalCodeService
		*emailService.EmailService
		*tokenService.TokenService
		*userService.UserService
		*eventService.EventService
		*exerciseService.ExerciseService
		*laboratoryService.LaboratoryService
	}

	IRepository interface {
		storageService.IRepository
		temporalCodeService.IRepository
		emailService.IRepository
		userService.IRepository
		eventService.IRepository
		exerciseService.IRepository
		laboratoryService.IRepository
	}

	Dependencies struct {
		Config     *config.ServiceConfig
		Repository IRepository
	}
)

func NewService(deps Dependencies) *Service {
	return &Service{
		OAuthService: oauthService.NewService(oauthService.Dependencies{Config: &deps.Config.OAuth}),
		Manager: password.NewHashManager(password.Dependencies{
			Cost:               deps.Config.Password.HashCost,
			PasswordComplexity: password.PasswordComplexityConfig(deps.Config.Password.PasswordComplexity),
		}),
		StorageService: storageService.NewService(storageService.Dependencies{Repository: deps.Repository, Config: &deps.Config.Storage}),
		TemporalCodeService: temporalCodeService.NewService(temporalCodeService.Dependencies{
			Repository: deps.Repository,
			Config:     &deps.Config.TemporalCode,
		}),
		EmailService: emailService.NewService(emailService.Dependencies{Repository: deps.Repository}),
		TokenService: tokenService.NewService(tokenService.Dependencies{
			Config: &deps.Config.JWT,
		}),
		UserService: userService.NewService(userService.Dependencies{Repository: deps.Repository}),
		EventService: eventService.NewService(eventService.Dependencies{
			Repository: deps.Repository,
		}),
		ExerciseService:   exerciseService.NewService(exerciseService.Dependencies{Repository: deps.Repository}),
		LaboratoryService: laboratoryService.NewService(laboratoryService.Dependencies{Repository: deps.Repository}),
	}
}
