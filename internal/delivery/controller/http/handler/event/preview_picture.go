package event

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

const maxPreviewPictureRequestBytes = (5 << 20) + (32 << 10)

func (h *Handler) uploadEventPreviewPicture(ctx *gin.Context) {
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
	pictureURL, err := h.useCase.UploadEventPreviewPicture(ctx.Request.Context(), eventID, claims.UserID, file, header.Size)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, struct {
		PreviewPicture string `json:"PreviewPicture"`
	}{PreviewPicture: pictureURL})
}

func (h *Handler) removeEventPreviewPicture(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := h.useCase.RemoveEventPreviewPicture(ctx.Request.Context(), eventID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithSuccess(ctx)
}

func (h *Handler) streamEventPreviewPicture(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithNotFound(ctx)
		return
	}
	reader, contentType, err := h.useCase.StreamEventPreviewPicture(ctx.Request.Context(), eventID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Disposition", "inline; filename=event-preview")
	ctx.Header("Content-Security-Policy", "default-src 'none'; img-src 'self'; sandbox")
	ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
	ctx.Header("Content-Type", contentType)
	ctx.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
