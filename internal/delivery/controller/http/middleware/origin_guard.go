package middleware

import (
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// OriginGuard is the anti-CSRF / anti-lab-page gate, on top of the session cookie's SameSite=Strict
// (which does not keep out pages on a domain that is same-site with the platform, like the lab
// device pages). It is an ALLOW-list, for every method:
//
//   - an Origin that is present must be on the allow-list (config.HostsConfig.OriginAllowed: MAIN,
//     ID, ADMIN, EXERCISES, API and one-label event sites, https only), else 403. "null" (sandboxed
//     frames, data: and file: pages, some redirects) is PRESENT and not allowed;
//   - without an Origin, a present Referer must be on the allow-list too, else 403;
//   - with neither: GET, HEAD and OPTIONS pass (navigations, the event frontend's server-side
//     fetches, health checks); a write (POST, PUT, PATCH, DELETE) is refused: a browser always
//     names its source on a cross-origin write, so a nameless write is not a browser acting for a
//     signed-in user (the API has no tokenless webhook);
//   - a write with a body must be application/json or multipart/form-data (the upload routes): a
//     cross-origin form post cannot send JSON, and a body in another type never reaches a handler
//     that reads JSON whatever its header says.
//
// The allow-list is the CORS one, shared, so the two cannot drift apart.
func OriginGuard(hosts config.HostsConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		write := isWrite(ctx.Request.Method)
		if origin, present := ctx.Request.Header["Origin"]; present && len(origin) > 0 {
			// Present (even empty or "null") is a named source.
			if reason, ok := hosts.OriginAllowed(origin[0]); !ok {
				refuse(ctx, "origin: "+reason)
				return
			}
		} else if referer := ctx.GetHeader("Referer"); referer != "" {
			if reason, ok := hosts.OriginAllowed(referer); !ok {
				refuse(ctx, "referer: "+reason)
				return
			}
		} else if write {
			refuse(ctx, "no Origin and no Referer on a state-changing request")
			return
		}
		if write && hasBody(ctx.Request) && !allowedBodyType(ctx.GetHeader("Content-Type")) {
			log.Warn().Str("method", ctx.Request.Method).Str("path", ctx.Request.URL.Path).
				Str("contentType", ctx.GetHeader("Content-Type")).Msg("Request refused: the body is not JSON")
			response.AbortWithUnsupportedMediaType(ctx)
			return
		}
		ctx.Next()
	}
}

func isWrite(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

func refuse(ctx *gin.Context, reason string) {
	log.Warn().Str("method", ctx.Request.Method).Str("path", ctx.Request.URL.Path).
		Str("origin", ctx.GetHeader("Origin")).Str("referer", ctx.GetHeader("Referer")).
		Str("reason", reason).Msg("Request refused by the origin guard")
	response.AbortWithForbidden(ctx)
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
