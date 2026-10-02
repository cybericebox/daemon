package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/errjournal"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// RateLimitPerUser allows limit requests of one signed-in user per window on the routes it guards. A request
// without a session is not counted: nothing is keyed on a client address (an on-site event puts hundreds of
// people behind one). A fixed window, in memory: enough to keep an expensive but legitimate action (a
// template preview, requested on every edit) from being used as a load generator.
func RateLimitPerUser(limit int, window time.Duration) gin.HandlerFunc {
	var (
		mu     sync.Mutex
		start  time.Time
		counts = map[string]int{}
	)
	return func(ctx *gin.Context) {
		claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
		if !ok {
			ctx.Next()
			return
		}
		key := claims.UserID.String()
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
