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
		UseCase IUseCase
		Config  *config.ControllerConfig
	}
)

func NewController(deps Dependencies) *Controller {
	return &Controller{
		httpController: http.NewController(http.Dependencies{
			Config:  &deps.Config.HTTP,
			UseCase: deps.UseCase,
		}),
	}
}

func (c *Controller) Start() {
	c.httpController.Start()
}

func (c *Controller) Stop(ctx context.Context) {
	c.httpController.Stop(ctx)
}
