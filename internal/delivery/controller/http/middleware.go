package http

import (
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
	"strings"
)

// getSubdomainIfValidDomain is a middleware that checks if the request host is a valid domain and sets the subdomain in the context
func getSubdomainIfValidDomain(domain string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !strings.HasSuffix(ctx.Request.Host, domain) {
			response.AbortWithNotFound(ctx)
			return
		}
		// get subdomain if exists (e.g. event.domain.com -> event, domain.com -> "")
		subdomain := strings.TrimSuffix(strings.TrimSuffix(ctx.Request.Host, domain), ".")
		ctx.Set(config.SubdomainCtxKey, subdomain)
	}
}
