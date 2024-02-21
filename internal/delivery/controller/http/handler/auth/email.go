package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/gin-gonic/gin"
)

// Email confirmation

type emailService interface {
	SendEmailConfirmation(ctx context.Context, email string) error
	ConfirmEmail(ctx context.Context, code string) error
}

type sendEmailConfirmationRequest struct {
	Email string `json:"email" binding:"required,email,max=255"`
}

func (h *Handler) sendEmailConfirmation(ctx *gin.Context) {
	var inp sendEmailConfirmationRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.service.SendEmailConfirmation(ctx, inp.Email); err != nil {
		if errors.Is(err, auth.ErrEmailAlreadyTaken) {
			response.AbortWithConflict(ctx, err)
			return
		}
		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}
	response.AbortWithOK(ctx)
}

func (h *Handler) confirmEmail(ctx *gin.Context) {
	code := ctx.Param("token")

	if err := h.service.ConfirmEmail(ctx, code); err != nil {
		if errors.Is(err, auth.ErrInvalidTemporalCode) {
			response.AbortWithUnauthorized(ctx, err)
			return
		}
		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}
	response.AbortWithOK(ctx)
}
