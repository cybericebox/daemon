package http

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/proxy"
	"github.com/gin-gonic/gin"
)

type (
	// Controller is the API for the application
	Controller struct {
		config  *config.HTTPConfig
		server  *Server
		service Service
	}

	// Service is the API for the service layer
	Service interface {
		// Service is dependencies for the http handler
		handler.Service
		// Service is dependencies for the http proxy
		proxy.Service
		// Service is dependencies for the routes protection
		protection.Service
	}

	// Dependencies for the controller
	Dependencies struct {
		Config  *config.HTTPConfig
		Service Service
	}
)

// NewController creates a new controller
func NewController(deps Dependencies) *Controller {
	controller := &Controller{
		config:  deps.Config,
		service: deps.Service,
	}

	// initialize routes protection
	protection.InitRoutesProtection(&protection.Dependencies{
		Config:  &deps.Config.Auth,
		Service: deps.Service,
	})

	// create the router
	router := gin.Default()

	// add global middleware
	router.Use(getSubdomainIfValidDomain(deps.Config.Server.Host))

	// create handler for routes on current service
	handler.NewAPIHandler(deps.Service).Init(router)

	// proxy to the service if no route is found
	router.NoRoute(proxy.HandleProxy(proxy.Dependencies{
		Config:  &deps.Config.Proxy,
		Service: deps.Service,
	}))

	// create the server
	controller.server = NewServer(&deps.Config.Server, router)

	return controller
}

// Start starts the controller
func (c *Controller) Start() {
	c.server.Start()
}

// Stop stops the controller
func (c *Controller) Stop(ctx context.Context) {
	c.server.Stop(ctx)
}
