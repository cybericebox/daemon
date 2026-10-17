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
// signed-in user has a bucket of their own. Requests without a session share two buckets (never per client
// address): public, for the routes a caller may use without a session (public reads, sign-in, the OAuth
// redirects), and closed, for the routes that need one and for paths no route matches. A flood of
// cookie-less 401s or 404s then spends closed and never the budget the public readers share. In memory, per
// replica.
type RateLimiter struct {
	mu        sync.Mutex
	users     map[uuid.UUID]*bucket
	public    bucket
	closed    bucket
	cfg       config.RateLimitConfig
	now       func() time.Time
	lastSweep time.Time
	observe   func(Bucket)
}

// Bucket names the budget a request is counted in.
type Bucket string

const (
	BucketUser   Bucket = "user"
	BucketPublic Bucket = "public"
	BucketClosed Bucket = "closed"
)

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(cfg config.RateLimitConfig) *RateLimiter {
	return &RateLimiter{cfg: cfg, users: map[uuid.UUID]*bucket{}, now: time.Now}
}

// Observe calls fn with the bucket of every request counted from now on (tests see where a request went).
func (l *RateLimiter) Observe(fn func(Bucket)) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.observe = fn
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

func (l *RateLimiter) allowAnonymous(name Bucket) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.observe != nil {
		l.observe(name)
	}
	if name == BucketPublic {
		return l.public.take(l.now(), l.cfg.AnonPerMinute, l.cfg.AnonBurst)
	}
	return l.closed.take(l.now(), l.cfg.ClosedPerMinute, l.cfg.ClosedBurst)
}

func (l *RateLimiter) allowUser(user uuid.UUID) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.observe != nil {
		l.observe(BucketUser)
	}
	now := l.now()
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
	b := l.users[user]
	if b == nil {
		b = &bucket{}
		l.users[user] = b
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

// ungatedPublicRoutes are the routes mounted without any permission gate (the public allowlist of
// tools/checkroutes): no gate counts their requests, so the global middleware counts them in public itself.
// Keyed "METHOD template"; a test in the handler package compares the set with the mounted router.
var ungatedPublicRoutes = map[string]bool{
	"POST /api/auth/sign-in":                true,
	"POST /api/auth/sign-up":                true,
	"GET /api/auth/setup":                   true,
	"POST /api/auth/setup":                  true,
	"POST /api/auth/password/reset-request": true,
	"POST /api/auth/password/reset":         true,
	"GET /api/auth/password/policy":         true,
	"POST /api/auth/account/email/confirm":  true,
	"GET /api/auth/avatar/:id":              true,
	"GET /api/auth/google":                  true,
	"GET /api/auth/google/register":         true,
	"GET /api/auth/google/setup":            true,
	"GET /api/auth/google/callback":         true,
	"GET /api/events/upcoming":              true,
}

// UngatedPublicRoutes are the "METHOD template" keys of the routes without a gate, counted in public.
func UngatedPublicRoutes() []string {
	routes := make([]string, 0, len(ungatedPublicRoutes))
	for route := range ungatedPublicRoutes {
		routes = append(routes, route)
	}
	return routes
}

const rateLimitCountedKey = "rateLimitCounted"

func (l *RateLimiter) refuse(ctx *gin.Context, wait time.Duration) {
	response.AbortWithTooManyRequests(ctx, wait)
}

// Anonymous is the global middleware. A request with a session cookie is left to the permission gate, which
// counts it once it knows who the caller is. Without a cookie: a path no route matches is counted in closed
// and an ungated public route in public here; a gated route is left to its gate, the one place that knows
// whether the route needs a session (closed) or not (public).
func (l *RateLimiter) Anonymous(ctx *gin.Context) {
	if exemptFromRateLimit[ctx.FullPath()] {
		ctx.Next()
		return
	}
	if cookie, err := ctx.Cookie(authModel.SessionCookie); err == nil && cookie != "" {
		ctx.Next()
		return
	}
	switch {
	case ctx.FullPath() == "":
		if !l.countAnonymous(ctx, BucketClosed) {
			return
		}
	case ungatedPublicRoutes[ctx.Request.Method+" "+ctx.FullPath()]:
		if !l.countAnonymous(ctx, BucketPublic) {
			return
		}
	}
	ctx.Next()
}

// countAnonymous counts a request without identity in name, once per request; it aborts with 429 and returns
// false when the bucket is empty.
func (l *RateLimiter) countAnonymous(ctx *gin.Context, name Bucket) bool {
	if _, counted := ctx.Get(rateLimitCountedKey); counted {
		return true
	}
	ctx.Set(rateLimitCountedKey, true)
	if ok, wait := l.allowAnonymous(name); !ok {
		l.refuse(ctx, wait)
		return false
	}
	return true
}

// CheckUser counts the request of a caller the gate has identified in that user's bucket; it aborts with 429
// and returns false when the bucket is empty.
func (l *RateLimiter) CheckUser(ctx *gin.Context, userID uuid.UUID) bool {
	if exemptFromRateLimit[ctx.FullPath()] {
		return true
	}
	if ok, wait := l.allowUser(userID); !ok {
		l.refuse(ctx, wait)
		return false
	}
	return true
}

// CheckAnonymous counts a request the gate found without identity (no cookie, or one that did not open): in
// public when the route serves callers without a session, in closed when it needs one, so the 401 that
// follows costs a token of closed. A request already counted (a second gate on the route) is not counted
// again. It aborts with 429 and returns false when the bucket is empty.
func (l *RateLimiter) CheckAnonymous(ctx *gin.Context, public bool) bool {
	if exemptFromRateLimit[ctx.FullPath()] {
		return true
	}
	name := BucketClosed
	if public {
		name = BucketPublic
	}
	return l.countAnonymous(ctx, name)
}
