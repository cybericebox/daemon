package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/gin-gonic/gin"
)

// Password reset

type passwordService interface {
	ForgotPassword(ctx context.Context, email string) error
	ResetPassword(ctx context.Context, code, newPassword string) error
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

	if err := h.service.ForgotPassword(ctx, inp.Email); err != nil {
		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}
	response.AbortWithOK(ctx)
}

type resetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=8,max=64"`
}

func (h *Handler) resetPassword(ctx *gin.Context) {
	code := ctx.Param("token")

	var inp resetPasswordRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.service.ResetPassword(ctx, code, inp.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidTemporalCode) {
			response.AbortWithUnauthorized(ctx, err)
			return
		}

		if errors.Is(err, auth.ErrInvalidPasswordComplexity) {
			response.AbortWithBadRequest(ctx, err)
			return
		}

		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}
	response.AbortWithOK(ctx)
}
