package auth

import (
	"context"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

type IGoogleUseCase interface {
	GetGoogleLoginURL() (string, error)
	GoogleAuth(ctx context.Context, code, state string) (*authModel.Tokens, error)
}

func (h *Handler) initOAuthGoogleAPIHandler(router *gin.RouterGroup) {
	google := router.Group("google")
	{
		google.GET("", h.googleOAuthRedirect)
		google.GET("callback", h.googleOAuthCallback)
	}
}

func (h *Handler) googleOAuthRedirect(ctx *gin.Context) {
	url, err := h.useCase.GetGoogleLoginURL()
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

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

	protection.SetAuthenticated(ctx, tokens, true)
}
