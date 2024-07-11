package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gin-gonic/gin"
)

type IGoogleUseCase interface {
	GetGoogleLoginURL() string
	GoogleAuth(ctx context.Context, code, state string) (*model.Tokens, error)
}

func (h *Handler) initOAuthGoogleAPIHandler(router *gin.RouterGroup) {
	google := router.Group("google")
	{
		google.GET("", h.googleOAuthRedirect)
		google.GET("callback", h.googleOAuthCallback)
	}
}

func (h *Handler) googleOAuthRedirect(ctx *gin.Context) {
	url := h.useCase.GetGoogleLoginURL()

	response.TemporaryRedirect(ctx, url)
}

func (h *Handler) googleOAuthCallback(ctx *gin.Context) {
	state := ctx.Query("state")
	code := ctx.Query("code")

	tokens, err := h.useCase.GoogleAuth(ctx, code, state)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	protection.SetAuthenticated(ctx, tokens)
}
