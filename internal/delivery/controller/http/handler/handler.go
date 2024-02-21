package handler

import (
	"github.com/gin-gonic/gin"
)

type (
	APIHandler struct {
		service Service
	}

	Service interface {
	}
)

func NewAPIHandler(service Service) *APIHandler {
	return &APIHandler{service: service}
}

func (h *APIHandler) Init(router *gin.Engine) {
	_ = router.Group("/api", corsMiddleware)
	{

	}
}
