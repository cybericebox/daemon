package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gin-gonic/gin"
)

type ISelfUseCase interface {
	GetSelfProfile(ctx context.Context) (*model.UserInfo, error)
}

func (h *Handler) initSelfAPIHandler(router *gin.RouterGroup) {
	self := router.Group("self", protection.RequireProtection)
	{
		self.GET("", h.getSelf)
	}
}

func (h *Handler) getSelf(ctx *gin.Context) {
	user, err := h.useCase.GetSelfProfile(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, user)
}
