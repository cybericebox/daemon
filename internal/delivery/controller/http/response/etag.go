package response

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// AbortWithETagData answers like AbortWithData, tagged with a hash of the data: a request
// whose If-None-Match still matches gets an empty 304. «private, no-cache» lets the browser
// keep the body and revalidate on every request. A positive freshFor (anonymous answers of
// public reads) lets it reuse the body without asking for that long first.
func AbortWithETagData(ctx *gin.Context, data any, freshFor time.Duration) {
	body, err := json.Marshal(data)
	if err != nil {
		AbortWithData(ctx, data)
		return
	}
	sum := sha256.Sum256(body)
	tag := `"` + hex.EncodeToString(sum[:16]) + `"`
	ctx.Header("ETag", tag)
	if freshFor > 0 {
		ctx.Header("Cache-Control", fmt.Sprintf("private, max-age=%d", int(freshFor.Seconds())))
		ctx.Header("Vary", "Cookie") // a sign-in must not be answered from the anonymous copy
	} else {
		ctx.Header("Cache-Control", "private, no-cache")
	}
	if ETagMatches(ctx.GetHeader("If-None-Match"), tag) {
		ctx.AbortWithStatus(http.StatusNotModified)
		return
	}
	AbortWithData(ctx, data)
}

// ETagMatches is the weak comparison of RFC 9110 §13.1.2 over a list.
func ETagMatches(header, tag string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == tag {
			return true
		}
	}
	return false
}
