package middleware

import (
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
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
		wait := window - now.Sub(start)
		mu.Unlock()
		if !allowed {
			response.AbortWithTooManyRequests(ctx, wait)
			return
		}
		ctx.Next()
	}
}

// RateLimiter is the general request limiter, a token bucket: refill rate per minute plus a burst. Every
// signed-in user has a bucket of their own; all anonymous requests share one (never per client address).
// In memory, per replica.
type RateLimiter struct {
	mu        sync.Mutex
	users     map[uuid.UUID]*bucket
	anon      bucket
	cfg       config.RateLimitConfig
	now       func() time.Time
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	return &RateLimiter{cfg: cfg, users: map[uuid.UUID]*bucket{}, now: time.Now}
}

// take spends one token of b (refilled at perMinute up to burst) and says how long to wait when empty.
func (b *bucket) take(now time.Time, perMinute, burst int) (bool, time.Duration) {
	if b.last.IsZero() {
		b.tokens = float64(burst)
	} else {
		b.tokens = min(float64(burst), b.tokens+now.Sub(b.last).Minutes()*float64(perMinute))
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	return false, time.Duration((1 - b.tokens) / float64(perMinute) * float64(time.Minute))
}

func (l *RateLimiter) allow(user *uuid.UUID) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if user == nil {
		return l.anon.take(now, l.cfg.AnonPerMinute, l.cfg.AnonBurst)
	}
	// An idle user's bucket is full again; forget it once a minute so the map stays small.
	if now.Sub(l.lastSweep) > time.Minute {
		l.lastSweep = now
		idle := time.Duration(float64(l.cfg.UserBurst) / float64(l.cfg.UserPerMinute) * float64(time.Minute))
		for id, b := range l.users {
			if now.Sub(b.last) > idle {
				delete(l.users, id)
			}
		}
	}
	b := l.users[*user]
	if b == nil {
		b = &bucket{}
		l.users[*user] = b
	}
	return b.take(now, l.cfg.UserPerMinute, l.cfg.UserBurst)
}

// exemptFromRateLimit: the live streams have their own budgets, and the health probe must always answer.
var exemptFromRateLimit = map[string]bool{
	"/api/health":              true,
	"/api/admin/errors/stream": true,
	"/api/events/:id/manage/solution-attempts/live": true,
	"/api/events/:id/results/live":                  true,
	"/api/events/self/live-screen/results/live":     true,
}

// RateLimitExemptRoutes are the route templates the general limiter skips (a test compares them with the router).
func RateLimitExemptRoutes() []string {
	routes := make([]string, 0, len(exemptFromRateLimit))
	for route := range exemptFromRateLimit {
		routes = append(routes, route)
	}
	return routes
}

const rateLimitCountedKey = "rateLimitCounted"

func (l *RateLimiter) refuse(ctx *gin.Context, name string, wait time.Duration) {
	response.AbortWithTooManyRequests(ctx, wait)
}

// Anonymous is the global middleware: a request without a session cookie is anonymous for sure and is
// counted in the one shared bucket. A request with a cookie is counted by Check, once the gate knows who
// the caller is.
func (l *RateLimiter) Anonymous(ctx *gin.Context) {
	if exemptFromRateLimit[ctx.FullPath()] {
		ctx.Next()
		return
	}
	if cookie, err := ctx.Cookie(authModel.SessionCookie); err == nil && cookie != "" {
		ctx.Next()
		return
	}
	ctx.Set(rateLimitCountedKey, true)
	if ok, wait := l.allow(nil); !ok {
		l.refuse(ctx, "anonymous", wait)
		return
	}
	ctx.Next()
}

// Check counts the request of a caller the gate has identified (signedIn) in that user's bucket, or an
// anonymous one that carried a cookie in the shared bucket; it aborts with 429 and returns false when the
// bucket is empty.
func (l *RateLimiter) Check(ctx *gin.Context, userID uuid.UUID, signedIn bool) bool {
	if exemptFromRateLimit[ctx.FullPath()] {
		return true
	}
	var key *uuid.UUID
	name := "user"
	if signedIn {
		key = &userID
	} else {
		if _, counted := ctx.Get(rateLimitCountedKey); counted {
			return true
		}
		name = "anonymous"
	}
	if ok, wait := l.allow(key); !ok {
		l.refuse(ctx, name, wait)
		return false
	}
	return true
}
