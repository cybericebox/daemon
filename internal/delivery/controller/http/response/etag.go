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
		AbortNotModified(ctx)
		return
	}
	AbortWithData(ctx, data)
}

// AbortNotModified answers an empty 304. The API-wide JSON Content-Type is dropped: a 304 may update
// the headers a cache stores, and it must not turn a stored image (or any other type) into JSON.
func AbortNotModified(ctx *gin.Context) {
	ctx.Writer.Header().Del("Content-Type")
	ctx.AbortWithStatus(http.StatusNotModified)
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

// AbortWithPublicETagData answers like AbortWithETagData for a body that is the same for every
// anonymous caller of the same URL and Origin: shared caches may keep it for freshFor
// («public, max-age»); it varies by Origin and Cookie, so a signed-in caller or another site never gets
// it. freshFor 0 only makes caches revalidate.
func AbortWithPublicETagData(ctx *gin.Context, data any, freshFor time.Duration) {
	body, err := json.Marshal(data)
	if err != nil {
		AbortWithData(ctx, data)
		return
	}
	sum := sha256.Sum256(body)
	ctx.Header("Vary", "Origin, Cookie")
	abortWithTag(ctx, data, `"`+hex.EncodeToString(sum[:16])+`"`, publicCacheControl(freshFor))
}

// AbortWithTaggedData answers with data under a caller-chosen ETag (a version string), 304 when the
// request's If-None-Match has it. public tells the answer is the same for every anonymous caller.
func AbortWithTaggedData(ctx *gin.Context, data any, version string, public bool) {
	cacheControl := "private, no-cache"
	if public {
		cacheControl = "public, no-cache"
		ctx.Header("Vary", "Origin, Cookie")
	}
	abortWithTag(ctx, data, `"`+version+`"`, cacheControl)
}

func publicCacheControl(freshFor time.Duration) string {
	if freshFor <= 0 {
		return "public, no-cache"
	}
	return fmt.Sprintf("public, max-age=%d", int(freshFor.Seconds()))
}

func abortWithTag(ctx *gin.Context, data any, tag, cacheControl string) {
	ctx.Header("ETag", tag)
	ctx.Header("Cache-Control", cacheControl)
	if ETagMatches(ctx.GetHeader("If-None-Match"), tag) {
		AbortNotModified(ctx)
		return
	}
	AbortWithData(ctx, data)
}
