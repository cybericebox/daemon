package eventself

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// abortWithETagData answers like response.AbortWithData, tagged with a hash
// of the data: a poll whose If-None-Match still matches gets an empty 304.
// «private, no-cache» lets the browser keep the body and revalidate on every
// request, so an unchanged poll costs no body and no JSON parsing.
func abortWithETagData(ctx *gin.Context, data any) {
	body, err := json.Marshal(data)
	if err != nil {
		response.AbortWithData(ctx, data)
		return
	}
	sum := sha256.Sum256(body)
	tag := `"` + hex.EncodeToString(sum[:16]) + `"`
	ctx.Header("ETag", tag)
	ctx.Header("Cache-Control", "private, no-cache")
	if etagMatches(ctx.GetHeader("If-None-Match"), tag) {
		ctx.AbortWithStatus(http.StatusNotModified)
		return
	}
	response.AbortWithData(ctx, data)
}

// etagMatches is the weak comparison of RFC 9110 §13.1.2 over a list.
func etagMatches(header, tag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimPrefix(strings.TrimSpace(candidate), "W/")
		if candidate == "*" || candidate == tag {
			return true
		}
	}
	return false
}
