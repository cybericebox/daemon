package event

import (
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// eventMedia describes how one kind of public event image is served.
type eventMedia struct {
	kind        eventUseCase.EventMediaKind
	disposition string
	csp         string
}

var (
	logoMedia    = eventMedia{eventUseCase.EventMediaLogo, "inline; filename=event-logo", "default-src 'none'; img-src 'self'; sandbox"}
	faviconMedia = eventMedia{eventUseCase.EventMediaFavicon, "inline; filename=event-favicon.png", "default-src 'none'; sandbox"}
	previewMedia = eventMedia{eventUseCase.EventMediaPreviewPicture, "inline; filename=event-preview", "default-src 'none'; img-src 'self'; sandbox"}
	contentMedia = eventMedia{eventUseCase.EventMediaContentImage, "inline; filename=event-banner", "default-src 'none'; img-src 'self'; sandbox"}
)

// serveEventMedia streams an event image. The file id in the URL is the image's version (a new upload is a
// new id and so a new URL), which is also its ETag. The database decides first whether the caller may see
// the image and whether the file belongs to the event; only then is a matching If-None-Match answered 304,
// without ever opening storage, and only a miss opens the bytes.
//
//   - event published: the URL never changes its content, so it is cached for a year, immutable;
//   - event hidden or closed (staff only): private, revalidated on every use, so withdrawal or revoked
//     staff access cannot reuse old bytes.
func (h *Handler) serveEventMedia(media eventMedia) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("Cache-Control", "private, no-store")
		eventID, ok := parseEventID(ctx)
		if !ok {
			return
		}
		fileID, err := uuid.FromString(ctx.Param("fileID"))
		if err != nil {
			response.AbortWithNotFound(ctx)
			return
		}
		public, err := h.useCase.CheckEventMedia(ctx.Request.Context(), media.kind, eventID, fileID)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		tag := `"event-image-` + fileID.String() + `"`
		ctx.Header("ETag", tag)
		if public {
			ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			ctx.Header("Cache-Control", "private, no-cache, must-revalidate, no-transform")
		}
		for _, line := range ctx.Request.Header.Values("If-None-Match") {
			if response.ETagMatches(line, tag) {
				ctx.AbortWithStatus(http.StatusNotModified)
				return
			}
		}
		reader, contentType, err := h.useCase.OpenEventMedia(ctx.Request.Context(), fileID)
		if err != nil {
			ctx.Header("ETag", "")
			ctx.Header("Cache-Control", "private, no-store")
			response.AbortWithError(ctx, err)
			return
		}
		defer func(r io.Closer) { _ = r.Close() }(reader)
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("Content-Disposition", media.disposition)
		ctx.Header("Content-Security-Policy", media.csp)
		ctx.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
	}
}
