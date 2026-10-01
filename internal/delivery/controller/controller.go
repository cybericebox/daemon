package controller

import (
	"context"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http"
)

type (
	Controller struct {
		httpController *http.Controller
	}

	IUseCase interface {
		http.IUseCase
	}

	Dependencies struct {
		UseCase              IUseCase
		HTTPControllerConfig config.HTTPControllerConfig
		AuthConfig           config.AuthConfig
	}
)

func NewController(deps Dependencies) *Controller {
	return &Controller{
		httpController: http.NewController(
			http.Dependencies{
				Config:     &deps.HTTPControllerConfig,
				UseCase:    deps.UseCase,
				AuthConfig: deps.AuthConfig,
			},
		),
	}
}

func (c *Controller) Start() {
	c.httpController.Start()
}

func (c *Controller) Stop(ctx context.Context) {
	c.httpController.Stop(ctx)
}
