package event

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

const maxLogoRequestBytes = (2 << 20) + (32 << 10)

func (h *Handler) uploadEventLogo(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	middleware.LimitBody(ctx, maxLogoRequestBytes)
	header, err := ctx.FormFile("file")
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	file, err := header.Open()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	defer func() { _ = file.Close() }()
	url, err := h.useCase.UploadEventLogo(ctx.Request.Context(), eventID, claims.UserID, file, header.Size)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, struct {
		LogoURL string `json:"LogoURL"`
	}{LogoURL: url})
}

func (h *Handler) removeEventLogo(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.RemoveEventLogo(ctx.Request.Context(), eventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithSuccess(ctx)
}
