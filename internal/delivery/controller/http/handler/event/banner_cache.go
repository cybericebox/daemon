package event

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

// Revalidate only after the use case has checked current publication/staff
// access and the event's file reference, and opened the blob successfully.
// Uploads create new file UUIDs backed by immutable content-addressed blobs;
// replacing a banner changes its URL and validator. Browsers must revalidate
// every reuse so withdrawal or revoked staff access cannot reuse old bytes.
func revalidateEventBanner(ctx *gin.Context, fileID uuid.UUID) bool {
	tag := `"event-image-` + fileID.String() + `"`
	ctx.Header("ETag", tag)
	ctx.Header("Cache-Control", "private, no-cache, must-revalidate, no-transform")
	for _, line := range ctx.Request.Header.Values("If-None-Match") {
		for _, candidate := range strings.Split(line, ",") {
			// GET If-None-Match uses weak comparison (RFC 9110, 13.1.2).
			candidate = strings.TrimSpace(candidate)
			if candidate == "*" || strings.TrimPrefix(candidate, "W/") == tag {
				ctx.AbortWithStatus(http.StatusNotModified)
				return true
			}
		}
	}
	return false
}
