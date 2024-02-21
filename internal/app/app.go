package app

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller"
	"github.com/rs/zerolog/log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func Run() {
	// Start the application
	cfg, valid := config.GetConfig()
	if !valid {
		log.Fatal().Msg("Configuration is not valid. Exiting...")
	}

	// Create the controller
	ctrl := controller.NewController(controller.Dependencies{
		Config:  &cfg.Controller,
		Service: nil,
	})

	// Start the server
	ctrl.Start()
	log.Info().Msg("Server started")

	// Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	<-quit

	const timeout = 5 * time.Second

	ctx, shutdown := context.WithTimeout(context.Background(), timeout)
	defer shutdown()

	ctrl.Stop(ctx)
}
