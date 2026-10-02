package event

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

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

func (h *Handler) streamEventLogo(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithNotFound(ctx)
		return
	}
	reader, contentType, err := h.useCase.StreamEventLogo(ctx.Request.Context(), eventID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Disposition", "inline; filename=event-logo")
	ctx.Header("Content-Security-Policy", "default-src 'none'; img-src 'self'; sandbox")
	ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
	ctx.Header("Content-Type", contentType)
	ctx.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
