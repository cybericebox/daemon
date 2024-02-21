package controller

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http"
)

type (
	// Controller is the API for the application
	Controller struct {
		config  *config.ControllerConfig
		service Service

		// httpController is the HTTP API for the controller
		httpController *http.Controller
	}

	// Service is the API for the service layer
	Service interface {

		// Service is dependencies for the http controller
		http.Service
	}

	// Dependencies for the controller
	Dependencies struct {
		Config  *config.ControllerConfig
		Service Service
	}
)

// NewController creates a new controller
func NewController(deps Dependencies) *Controller {
	return &Controller{
		config:  deps.Config,
		service: deps.Service,
		httpController: http.NewController(http.Dependencies{
			Config:  &deps.Config.HTTP,
			Service: deps.Service,
		}),
	}
}

// Start starts the controller
func (c *Controller) Start() {
	c.httpController.Start()
}

// Stop stops the controller
func (c *Controller) Stop(ctx context.Context) {
	c.httpController.Stop(ctx)
}
