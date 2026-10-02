// Package cors implements the cross-origin policy for api.<domain>: every
// platform frontend (main/id/admin/event) lives on its own subdomain and calls
// this API cross-origin with credentials, so browsers require an explicit,
// non-wildcard CORS response before they will expose it — or send the session
// cookie on the request in the first place.
//
// The same check doubles as the anti-CSRF gate: a request that carries an
// Origin header not covered by the platform domain is rejected outright,
// before touching any handler. Requests with no Origin header (non-browser
// clients, and the handful of browser request types that never carry one —
// <img>/<script>/plain navigation) pass through unchanged; none of them can
// carry a JSON body or custom headers, so none can reach a mutating endpoint
// in this API — this asymmetry does not weaken the CSRF protection.
package middleware

import (
	"github.com/cybericebox/daemon/internal/config"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
)

const preflightMaxAge = 10 * time.Minute

// HandleCORSMiddleWare returns a gin middleware that allows cross-origin, credentialed
// requests from the exact frontend hosts (main, ID, admin, exercises) and from
// https://<tag>.<EVENT_DOMAIN> event sites. Any other present Origin is rejected
// with 403 before the request reaches routing.
func HandleCORSMiddleWare(hosts config.HostsConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		origin := ctx.GetHeader("Origin")
		if origin == "" {
			ctx.Next()
			return
		}

		if reason, ok := originAllowed(origin, hosts); !ok {
			log.Warn().
				Str("origin", origin).
				Str("eventDomain", hosts.EventDomain).
				Str("reason", reason).
				Str("method", ctx.Request.Method).
				Str("path", ctx.Request.URL.Path).
				Msg("CORS: origin rejected (403) — not on the platform domain allowlist")
			response.AbortWithForbidden(ctx)
			return
		}

		ctx.Header("Access-Control-Allow-Origin", origin)
		ctx.Header("Access-Control-Allow-Credentials", "true")
		ctx.Header("Vary", "Origin")
		// Cross-origin JS may read only the headers listed here: the sign-in
		// redirect URL, Content-Disposition (export archive filename) and
		// Retry-After (flag submission rate limit).
		ctx.Header("Access-Control-Expose-Headers", protection.SignInURLHeader+", Content-Disposition, Retry-After")

		if ctx.Request.Method == http.MethodOptions {
			ctx.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, PUT, DELETE, OPTIONS")
			ctx.Header("Access-Control-Allow-Headers", "Content-Type")
			ctx.Header("Access-Control-Max-Age", strconv.Itoa(int(preflightMaxAge.Seconds())))
			response.AbortWithNoContent(ctx)
			return
		}

		ctx.Next()
	}
}

// originAllowed delegates to the shared allow-list (config.HostsConfig.OriginAllowed), the same one
// the origin guard uses.
func originAllowed(origin string, hosts config.HostsConfig) (reason string, ok bool) {
	return hosts.OriginAllowed(origin)
}
