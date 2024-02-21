package auth

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/gin-gonic/gin"
)

type signService interface {
	SignIn(ctx context.Context, email, password string) (*auth.Tokens, error)
}

type signInRequest struct {
	Email    string `json:"email" binding:"required,email,max=255"`
	Password string `json:"password" binding:"required,min=8,max=64"`
}

func (h *Handler) signIn(ctx *gin.Context) {
	var inp signInRequest
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	tokens, err := h.service.SignIn(ctx, inp.Email, inp.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidEmailOrPassword) {
			response.AbortWithUnauthorized(ctx, err)
			return
		}
		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}

	protection.SetTokens(ctx, tokens)

	response.AbortWithOK(ctx)
}

func (h *Handler) signOut(ctx *gin.Context) {
	protection.UnsetTokens(ctx)
	response.TemporalRedirect(ctx, "/")
}
