package auth

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

type IPasswordUseCase interface {
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, code, newPassword string) error
}

func (h *Handler) initPasswordAPIHandler(router *gin.RouterGroup) {
	password := router.Group("password")
	{
		password.POST("forgot", protection.RequireRecaptcha("forgotPassword"), h.forgotPassword)
		password.POST("reset/:code", h.resetPassword)
	}
}

func (h *Handler) forgotPassword(ctx *gin.Context) {
	var inp userModel.User

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.ForgotPassword(ctx, inp.Email); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) resetPassword(ctx *gin.Context) {
	code := ctx.Param("code")

	var inp userModel.User

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.ResetPassword(ctx, code, inp.Password); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
