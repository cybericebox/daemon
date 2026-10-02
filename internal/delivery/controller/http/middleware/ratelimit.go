package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/errjournal"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// RateLimitPerUser allows limit requests of one signed-in user per window on the routes it guards (a client
// address stands in for a request without a session). A fixed window, in memory: enough to keep an
// expensive but legitimate action (a template preview, requested on every edit) from being used as a load
// generator.
func RateLimitPerUser(limit int, window time.Duration) gin.HandlerFunc {
	var (
		mu     sync.Mutex
		start  time.Time
		counts = map[string]int{}
	)
	return func(ctx *gin.Context) {
		key := ctx.ClientIP()
		if claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); ok {
			key = claims.UserID.String()
		}
		mu.Lock()
		now := time.Now()
		if now.Sub(start) >= window {
			start, counts = now, map[string]int{}
		}
		counts[key]++
		allowed := counts[key] <= limit
		mu.Unlock()
		if !allowed {
			errjournal.SetLimiter(ctx, "per-user")
			response.AbortWithTooManyRequests(ctx)
			return
		}
		ctx.Next()
	}
}
