package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
)

type IPasswordUseCase interface {
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, code, newPassword string) error
	ChangePassword(ctx context.Context, oldPassword, newPassword string) error
}

func (h *Handler) initPasswordAPIHandler(router *gin.RouterGroup) {
	password := router.Group("password")
	{
		password.POST("", protection.RequireProtection, h.changePassword)
		password.POST("forgot", protection.RequireRecaptcha("forgotPassword"), h.forgotPassword)
		password.POST("reset/:code", h.resetPassword)
	}
}

type changePasswordRequest struct {
	OldPassword string `json:"oldPassword" binding:"required,min=1,max=64"`
	NewPassword string `json:"newPassword" binding:"required,min=1,max=64"`
}

func (h *Handler) changePassword(ctx *gin.Context) {
	var inp changePasswordRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.ChangePassword(ctx, inp.OldPassword, inp.NewPassword); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Password changed successfully")

}

type forgotPasswordRequest struct {
	Email string `json:"email" binding:"required,email,max=255"`
}

func (h *Handler) forgotPassword(ctx *gin.Context) {
	var inp forgotPasswordRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.ForgotPassword(ctx, inp.Email); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Password reset link sent successfully")
}

type resetPasswordRequest struct {
	Password string `json:"newPassword" binding:"required,min=1,max=64"`
}

func (h *Handler) resetPassword(ctx *gin.Context) {
	code := ctx.Param("code")

	var inp resetPasswordRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.ResetPassword(ctx, code, inp.Password); err != nil {
		response.AbortWithError(ctx, err)
	}
	response.AbortWithOK(ctx, "Password reset successfully")
}
