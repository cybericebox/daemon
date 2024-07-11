package http

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/errorWrapper"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/proxy"
	"github.com/gin-gonic/gin"
)

type (
	Controller struct {
		server *Server
	}

	IUseCase interface {
		// IUseCase is dependencies for the http handler
		handler.IUseCase
		// IUseCase is dependencies for the http proxy
		proxy.IUseCase
		// IUseCase is dependencies for the routes protection
		protection.IUseCase

		URLNeedsProtection(ctx context.Context, url string) bool
	}

	Dependencies struct {
		UseCase IUseCase
		Config  *config.HTTPConfig
	}
)

func NewController(deps Dependencies) *Controller {
	// create the router
	router := gin.Default()

	// initialize protection
	protection.InitProtection(&protection.Dependencies{
		Config:  &deps.Config.Protection,
		UseCase: deps.UseCase,
	})

	// add global middleware for error handling
	router.Use(errorWrapper.WithErrorHandler)

	// add global middleware for validating if the request domain is equal to the domain of the platform
	router.Use(protection.ValidateRequestDomain)

	// create handler for routes on current service
	handler.NewAPIHandler(deps.UseCase).Init(router)

	//proxy sign-in and profile pages to main frontend
	router.Use(proxy.HandleProxyToMainPages())

	// frontends that need protection
	protectFrontends := func(ctx *gin.Context) bool {
		return deps.UseCase.URLNeedsProtection(ctx, ctx.Request.URL.Path)
	}

	//proxy to frontends
	router.NoRoute(
		protection.DynamicallyRequireProtection(protectFrontends, true),
		proxy.HandleProxy(proxy.Dependencies{
			Config:  &deps.Config.Proxy,
			UseCase: deps.UseCase,
		}))

	return &Controller{
		server: NewServer(&deps.Config.Server, router),
	}
}

func (c *Controller) Start() {
	c.server.Start()
}

func (c *Controller) Stop(ctx context.Context) {
	c.server.Stop(ctx)
}
