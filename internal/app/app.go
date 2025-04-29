package app

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/model"
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

	// task for debugging
	w.AddTask(worker.NewTask().WithDo(func() {
		stat := repo.PGStat()
		total := stat.TotalConns()
		idle := stat.IdleConns()
		inUse := stat.AcquiredConns()
		maxCon := stat.MaxConns()
		newCon := stat.NewConnsCount()
		log.Info().Int("total", int(total)).Int("idle", int(idle)).Int("inUse", int(inUse)).Int("maxCon", int(maxCon)).Int("newCon", int(newCon)).Msg("Postgres pool stats")
	}).WithRepeatDuration(10 * time.Second).Create())

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
	if err := u.InitEventsHooks(ctx); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to initialize events hooks").Cause()
	}

	log.Info().Msg("Events hooks are initialized")

	// initialize platform hooks
	u.InitPlatformHooks(ctx)

	log.Info().Msg("Platform hooks are initialized")

	log.Info().Msg("Application workers are initialized")

	return nil
}
