package event

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
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
	middleware.LimitBody(ctx, maxPreviewPictureRequestBytes)
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
