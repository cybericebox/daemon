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
	UpdatePassword(ctx context.Context, oldPassword, newPassword string) error
	UpdateSelfProfile(ctx context.Context, user model.User) error
}

func (h *Handler) initSelfAPIHandler(router *gin.RouterGroup) {
	selfAPI := router.Group("self", protection.RequireProtection())
	{
		profileAPI := selfAPI.Group("profile")
		{
			profileAPI.GET("", h.getProfile)
			profileAPI.PUT("", h.updateProfile)
		}
		selfAPI.PATCH("password", h.updatePassword)

	}
}

func (h *Handler) getProfile(ctx *gin.Context) {
	user, err := h.useCase.GetSelfProfile(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, user)
}

func (h *Handler) updateProfile(ctx *gin.Context) {
	var inp model.User

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.UpdateSelfProfile(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

type updatePasswordRequest struct {
	OldPassword string `binding:"required,min=1,max=64"`
	NewPassword string `binding:"required,min=1,max=64"`
}

func (h *Handler) updatePassword(ctx *gin.Context) {
	var inp updatePasswordRequest

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.UpdatePassword(ctx, inp.OldPassword, inp.NewPassword); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
