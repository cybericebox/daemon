package app

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/service"
	"github.com/cybericebox/daemon/internal/useCase"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/rs/zerolog/log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func Run() {
	// Start the application
	cfg := config.MustGetConfig()

	// Create the repository
	repo := repository.NewRepository(repository.Dependencies{
		Config: &cfg.Repository,
	})

	// Create the service
	services := service.NewService(service.Dependencies{
		Repository: repo,
		Config:     &cfg.Service,
	})

	// Worker initialization

	w := worker.NewWorker(cfg.Service.MaxWorkers)

	useCases := useCase.NewUseCase(
		useCase.Dependencies{
			Service: services,
			Worker:  w,
		})

	// Create the controller
	ctrl := controller.NewController(controller.Dependencies{
		Config:  &cfg.Controller,
		UseCase: useCases,
	})

	// Initialize the application
	if err := InitWorkers(useCases); err != nil {
		log.Fatal().Err(err).Msg("Application workers initialization failed")
	}

	// Start nginx UDP reverse proxy
	if cfg.Environment != config.Local {
		if err := exec.Command("/bin/sh", "-c", "service nginx start").Run(); err != nil {
			log.Fatal().Err(err).Msg("Starting nginx UDP reverse proxy failed")
		}
	}

	// Start the server
	ctrl.Start()
	log.Info().Msg("Server started")

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	<-quit

	const timeout = 5 * time.Second

	ctx, shutdown := context.WithTimeout(context.Background(), timeout)
	defer shutdown()

	ctrl.Stop(ctx)

}

func InitWorkers(u *useCase.UseCase) error {
	// Initialize the application workers
	ctx := context.Background()
	// create the teams challenges for already started events
	if err := u.CreateEventTeamsChallengesTasks(ctx); err != nil {
		return err
	}
	log.Info().Msg("All challenges added to already started events")

	log.Info().Msg("Application workers are initialized")
	return nil
}
