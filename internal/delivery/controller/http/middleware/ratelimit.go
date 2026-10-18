package middleware

import (
	"net/http"
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
	// ungated holds the "METHOD template" keys of UngatedPublicRoutes.
	ungated map[string]bool
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
	l := &RateLimiter{
		cfg:     cfg,
		users:   map[uuid.UUID]*bucket{},
		now:     time.Now,
		ungated: map[string]bool{},
	}
	for _, route := range UngatedPublicRoutes() {
		l.ungated[route] = true
	}
	return l
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

// UngatedPublicRoutes are the "METHOD template" keys of the routes mounted without any permission gate (the
// public allowlist of tools/checkroutes): no gate counts their requests, so the global middleware counts them
// in public itself. A test in the handler package compares the list with the mounted router.
func UngatedPublicRoutes() []string {
	return []string{
		"POST /api/auth/sign-in",
		"POST /api/auth/sign-up",
		"GET /api/auth/setup",
		"POST /api/auth/setup",
		"POST /api/auth/password/reset-request",
		"POST /api/auth/password/reset",
		"GET /api/auth/password/policy",
		"POST /api/auth/account/email/confirm",
		"GET /api/auth/avatar/:id",
		"GET /api/auth/google",
		"GET /api/auth/google/register",
		"GET /api/auth/google/setup",
		"GET /api/auth/google/callback",
		"GET /api/events/upcoming",
	}
}

const (
	// rateLimitCountedKey marks a request that is counted (or, on the internal listener, exempt) in the
	// anonymous buckets, or already counted in its user's bucket.
	rateLimitCountedKey = "rateLimitCounted"
	// rateLimitUserCountedKey marks a request counted in its user's bucket.
	rateLimitUserCountedKey = "rateLimitUserCounted"
)

func (l *RateLimiter) refuse(ctx *gin.Context, wait time.Duration) {
	response.AbortWithTooManyRequests(ctx, wait)
}

// Anonymous is the global middleware, in front of the in-flight limiter: a request it refuses never takes a
// slot nor has its body read. It counts every request without a session cookie, in the bucket of its route
// (anonymousBucket), so the gate later finds it counted. A request with a cookie is counted here only where
// no identity can ever be established (a path no route matches, an ungated route); elsewhere the gate counts
// it once it knows who the caller is. The internal listener (in-cluster SSR: one trusted caller in front of
// many visitors) is not counted in the anonymous buckets; the in-flight cap bounds it.
func (l *RateLimiter) Anonymous(ctx *gin.Context) {
	if exemptFromRateLimit[ctx.FullPath()] {
		ctx.Next()
		return
	}
	if OnInternalListener(ctx.Request.Context()) {
		ctx.Set(rateLimitCountedKey, true)
		ctx.Next()
		return
	}
	if name, ok := l.anonymousBucket(ctx); ok && !l.countAnonymous(ctx, name) {
		return
	}
	ctx.Next()
}

// anonymousBucket is the bucket the global middleware counts a request in; false leaves it to the gate (a
// gated route, with a session cookie). Without a cookie: public on an ungated route and on a GET/HEAD of a
// public read (PublicReadRoutes: the routes gated only by permissions RolePublic holds), closed on every other
// route and on a path no route matches.
func (l *RateLimiter) anonymousBucket(ctx *gin.Context) (Bucket, bool) {
	path := ctx.FullPath()
	switch {
	case path == "":
		return BucketClosed, true
	case l.ungated[ctx.Request.Method+" "+path]:
		return BucketPublic, true
	}
	if cookie, err := ctx.Cookie(authModel.SessionCookie); err == nil && cookie != "" {
		return "", false
	}
	if method := ctx.Request.Method; (method == http.MethodGet || method == http.MethodHead) &&
		PublicReadRoutes[path] {
		return BucketPublic, true
	}
	return BucketClosed, true
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

// CheckUser counts the request of a caller the gate has identified in that user's bucket, once per request
// however many gates the route has (and the request is then not counted as anonymous too); it aborts with 429
// and returns false when the bucket is empty.
func (l *RateLimiter) CheckUser(ctx *gin.Context, userID uuid.UUID) bool {
	if exemptFromRateLimit[ctx.FullPath()] {
		return true
	}
	if _, counted := ctx.Get(rateLimitUserCountedKey); counted {
		return true
	}
	ctx.Set(rateLimitUserCountedKey, true)
	ctx.Set(rateLimitCountedKey, true)
	if ok, wait := l.allowUser(userID); !ok {
		l.refuse(ctx, wait)
		return false
	}
	return true
}

// CheckAnonymous counts a request the gate found without identity (no cookie, or one that did not open): in
// public when the route serves callers without a session, in closed when it needs one, so the 401 that
// follows costs a token of closed. A request already counted (by the global middleware, by an earlier gate,
// or in its user's bucket) is not counted again. It aborts with 429 and returns false when the bucket is empty.
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
