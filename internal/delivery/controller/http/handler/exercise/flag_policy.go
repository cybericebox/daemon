package exercise

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

func (h *Handler) flagPolicy(ctx *gin.Context) {
	response.AbortWithData(ctx, h.useCase.FlagPolicy())
}
