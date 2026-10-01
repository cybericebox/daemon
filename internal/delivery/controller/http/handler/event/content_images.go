package event

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

func (h *Handler) uploadEventContentImage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, maxPreviewPictureRequestBytes)
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
	imageURL, err := h.useCase.UploadEventContentImage(ctx.Request.Context(), eventID, claims.UserID, file, header.Size)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, struct {
		ImageURL string `json:"ImageURL"`
	}{ImageURL: imageURL})
}

func (h *Handler) streamEventContentImage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithNotFound(ctx)
		return
	}
	reader, contentType, err := h.useCase.StreamEventContentImage(ctx.Request.Context(), eventID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Disposition", "inline; filename=event-banner")
	ctx.Header("Content-Security-Policy", "default-src 'none'; img-src 'self'; sandbox")
	ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
	ctx.Header("Content-Type", contentType)
	ctx.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
