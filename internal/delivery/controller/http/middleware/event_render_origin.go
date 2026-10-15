package middleware

import (
	"net/url"
	"strings"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/gin-gonic/gin"
)

// IsEventRenderRoute deliberately matches only the four new endpoints, including
// their preflights (which have no matched Gin route template).
func IsEventRenderRoute(path string) bool {
	switch path {
	case "/api/auth/event-render-key", "/api/events/self/render-grant", "/api/events/self/render-manifest", "/api/events/self/render-page":
		return true
	}
	return false
}

// Event existence is private on these endpoints. Validate only the exact owned
// origin syntax here; tenant resolution and entitlement remain in the handler.
func renderOriginAllowed(hosts config.HostsConfig, origin string) (string, bool) {
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" || origin != "https://"+strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".") {
		return "origin is not canonical", false
	}
	return hosts.OriginAllowed(origin)
}
func renderResponseHeaders(ctx *gin.Context) {
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Header("Vary", "Origin, Authorization, Cookie")
}
