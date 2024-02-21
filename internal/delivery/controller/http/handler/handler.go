package handler

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/gin-gonic/gin"
)

type (
	Handler struct {
		service Service
	}

	Service interface {
		auth.Service
	}
)

func NewAPIHandler(service Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Init(router *gin.Engine) {
	api := router.Group("/api", corsMiddleware)
	{
		auth.NewAuthAPIHandler(h.service).Init(api)
	}
}
