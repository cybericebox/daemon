package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gin-gonic/gin"
)

type IEmailUseCase interface {
	ChangeEmail(ctx context.Context, email string) error
	ConfirmEmail(ctx context.Context, code string) error
}

func (h *Handler) initEmailAPIHandler(router *gin.RouterGroup) {
	email := router.Group("email")
	{
		email.PUT("", protection.RequireProtection(), h.changeEmail)
		email.POST("confirm/:code", h.confirmEmail)
	}
}

func (h *Handler) changeEmail(ctx *gin.Context) {
	var inp model.User

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.ChangeEmail(ctx, inp.Email); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) confirmEmail(ctx *gin.Context) {
	code := ctx.Param("code")

	if err := h.useCase.ConfirmEmail(ctx, code); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
