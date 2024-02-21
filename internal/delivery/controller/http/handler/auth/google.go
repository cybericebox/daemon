package auth

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/auth"
	"github.com/gin-gonic/gin"
	"net/http"
)

type googleService interface {
	GetGoogleLoginURL() string
	GoogleAuth(ctx context.Context, code, state string) (*auth.Tokens, error)
}

// Google OAuth
func (h *Handler) googleOAuthRedirect(ctx *gin.Context) {
	url := h.service.GetGoogleLoginURL()
	ctx.Redirect(http.StatusTemporaryRedirect, url)
}

func (h *Handler) googleOAuthCallback(ctx *gin.Context) {
	state := ctx.Query("state")
	code := ctx.Query("code")

	tokens, err := h.service.GoogleAuth(ctx, code, state)

	if err != nil {
		response.LogAndAbortWithInternalServerError(ctx, err)
		return
	}

	protection.SetTokens(ctx, tokens)

	from := protection.GetFromURL(ctx)

	response.TemporalRedirect(ctx, from)
}
