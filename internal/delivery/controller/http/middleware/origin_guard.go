package middleware

import (
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// OriginGuard is the anti-CSRF gate for state-changing requests (POST, PUT, PATCH, DELETE) on top
// of the session cookie's SameSite=Strict:
//
//   - a request from a page under LABS_DOMAIN is refused. Lab device pages run content a task
//     controls, on a domain that is same-site with the platform, so SameSite does not keep them out;
//   - a request that names neither Origin nor Referer is refused: a browser always sends one of them
//     on a cross-origin write, so a write without either is not a browser acting for a signed-in
//     user (the API has no webhook or other tokenless caller);
//   - a request with a body must be application/json (or multipart/form-data, which the upload
//     routes take): a cross-origin form post cannot send JSON, and a body in another type never
//     reaches a handler that reads JSON whatever its header says.
//
// Safe methods pass untouched.
func OriginGuard(hosts config.HostsConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		switch ctx.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			ctx.Next()
			return
		}
		source, present := requestSource(ctx.Request)
		switch {
		case !present:
			refuse(ctx, "no Origin and no Referer on a state-changing request")
			return
		case hosts.IsLabsHost(source):
			refuse(ctx, "request from the labs domain")
			return
		}
		if hasBody(ctx.Request) && !allowedBodyType(ctx.GetHeader("Content-Type")) {
			log.Warn().Str("method", ctx.Request.Method).Str("path", ctx.Request.URL.Path).
				Str("contentType", ctx.GetHeader("Content-Type")).Msg("Request refused: the body is not JSON")
			response.AbortWithUnsupportedMediaType(ctx)
			return
		}
		ctx.Next()
	}
}

func refuse(ctx *gin.Context, reason string) {
	log.Warn().Str("method", ctx.Request.Method).Str("path", ctx.Request.URL.Path).
		Str("origin", ctx.GetHeader("Origin")).Str("referer", ctx.GetHeader("Referer")).
		Str("reason", reason).Msg("Request refused by the origin guard")
	response.AbortWithForbidden(ctx)
}

// requestSource is the host the request comes from: the Origin, else the Referer. present is false
// when neither is there (or neither can be read: an unreadable value is as good as none).
func requestSource(r *http.Request) (host string, present bool) {
	for _, header := range []string{"Origin", "Referer"} {
		raw := r.Header.Get(header)
		if raw == "" {
			continue
		}
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			// "null" (a sandboxed page) and garbage: it names a source, just not a usable one.
			return strings.ToLower(raw), true
		}
		return strings.ToLower(u.Hostname()), true
	}
	return "", false
}

func hasBody(r *http.Request) bool {
	return r.ContentLength > 0 || (r.ContentLength == -1 && r.Body != nil && r.Body != http.NoBody)
}

func allowedBodyType(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || mediaType == "multipart/form-data"
}
