package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
)

type IEmailUseCase interface {
	ConfirmEmail(ctx context.Context, code string) error
}

func (h *Handler) initEmailAPIHandler(router *gin.RouterGroup) {
	email := router.Group("email")
	{
		email.POST("confirm/:code", h.confirmEmail)
	}
}

func (h *Handler) confirmEmail(ctx *gin.Context) {
	code := ctx.Param("code")

	if err := h.useCase.ConfirmEmail(ctx, code); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
