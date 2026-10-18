package event

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
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
