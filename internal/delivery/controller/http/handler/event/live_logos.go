package event

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// uploadLiveLogo godoc
// @Summary Upload a Live logo (SVG sanitized, PNG or WebP, up to 1 MB; the content is sniffed)
// @Tags events
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "event ID"
// @Param file formData file true "logo"
// @Success 200 {object} response.Response{data=object{ImageURL=string}}
// @Router /events/{id}/manage/content/live/logos [post]
func (h *Handler) uploadLiveLogo(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 2<<20)
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
	imageURL, err := h.useCase.UploadLiveLogo(ctx.Request.Context(), eventID, claims.UserID, file, header.Size)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, struct {
		ImageURL string `json:"ImageURL"`
	}{ImageURL: imageURL})
}
