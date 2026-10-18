package middleware

import (
	"mime"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// OriginGuard decides by the sensitivity of the data, not by the method alone, on top of the session
// cookie's SameSite=Strict (which does not keep out pages on a domain that is same-site with the
// platform, like the lab device pages):
//
//   - PUBLIC reads (GET/HEAD of PublicReadRoutes: public event info and content, the scoreboard,
//     public media, avatars, the OAuth redirects) are not checked: embeds and referrers must never
//     break;
//   - everything else (a gated route, a write) must come from the allow-list (OriginPolicy: the
//     platform hosts and event sites whose tag exists, https only): a present Origin must be allowed,
//     else 403 ("null" is present and never allowed); with no Origin a present Referer must be
//     allowed; with neither, a WRITE is refused (a browser always names its source on a cross-origin
//     write; the API has no tokenless webhook), and a READ is allowed except a no-cors subresource
//     load (Sec-Fetch-Mode: no-cors: an <img> or <script> of an authenticated URL), while top-level
//     navigations, downloads and server-side fetches pass;
//   - a write with a body must be application/json or multipart/form-data (the upload routes): a
//     cross-origin form post cannot send JSON, and a body in another type never reaches a handler
//     that reads JSON whatever its header says. The one other type is application/octet-stream, and
//     only on BinaryBodyRoutes (the handlers that read the raw body, the upload chunks): it is not a
//     CORS-simple type, so a cross-origin form cannot send it without a preflight, which the CORS
//     policy refuses for foreign origins.
func OriginGuard(hosts config.HostsConfig) gin.HandlerFunc {
	return OriginGuardWith(OriginPolicy{Hosts: hosts})
}

// OriginGuardWith is OriginGuard over an explicit policy (with the event tag check).
func OriginGuardWith(policy OriginPolicy) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		write := isWrite(ctx.Request.Method)
		if !write && PublicReadRoutes[ctx.FullPath()] {
			ctx.Next()
			return
		}
		req := ctx.Request.Context()
		allowed := func(origin string) (string, bool) {
			if IsEventRenderRoute(ctx.Request.URL.Path) {
				return renderOriginAllowed(policy.Hosts, origin)
			}
			return policy.Allowed(req, origin)
		}
		if origin, present := ctx.Request.Header["Origin"]; present && len(origin) > 0 {
			// Present (even empty or "null") is a named source.
			if reason, ok := allowed(origin[0]); !ok {
				refuse(ctx, "origin: "+reason)
				return
			}
		} else if referer := ctx.GetHeader("Referer"); referer != "" {
			if reason, ok := allowed(referer); !ok {
				refuse(ctx, "referer: "+reason)
				return
			}
		} else if write {
			refuse(ctx, "no Origin and no Referer on a state-changing request")
			return
		} else if ctx.GetHeader("Sec-Fetch-Mode") == "no-cors" {
			refuse(ctx, "no-cors load of an authenticated URL")
			return
		}
		if write && hasBody(ctx.Request) && !allowedBodyType(ctx.GetHeader("Content-Type"), BinaryBodyRoutes[ctx.FullPath()]) {
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

func allowedBodyType(contentType string, binary bool) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return mediaType == "application/json" || mediaType == "multipart/form-data" ||
		(binary && mediaType == "application/octet-stream")
}

// BinaryBodyRoutes are the route templates whose handler reads the raw request body (an upload chunk),
// so application/octet-stream is accepted there and nowhere else. Keep this set and the routes that use
// the RawBody middleware identical: a test in the handler package compares them.
var BinaryBodyRoutes = map[string]bool{
	"/api/exercises/uploads/:uploadID/chunks/:index": true,
}

// RawBody marks a route whose handler reads the raw request body (see BinaryBodyRoutes). It changes
// nothing itself; the origin guard looks the route up in BinaryBodyRoutes.
func RawBody(c *gin.Context) {
	c.Next()
}
