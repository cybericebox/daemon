package handler

import (
	"github.com/gin-gonic/gin"
)

type (
	Handler struct {
		useCase IUseCase
	}

	IUseCase interface {
	}
)

func NewAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.Engine) {
	_ = router.Group("api", corsMiddleware)
	{

	}
}

func corsMiddleware(ctx *gin.Context) {
	ctx.Header("Content-Type", "application/json")
}
