package middleware_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// signedIn is a stand-in for the authentication middleware: the user comes from the X-User header.
func signedIn(c *gin.Context) {
	if id := c.GetHeader("X-User"); id != "" {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uuid.FromStringOrNil(id)}))
	}
	c.Next()
}

func TestRateLimitPerUserAllowsTheLimitThenRefuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(signedIn)
	r.POST("/preview", middleware.RateLimitPerUser(3, time.Minute), func(c *gin.Context) { c.Status(http.StatusOK) })
	var codes []int
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/preview", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		req.Header.Set("X-User", "00000000-0000-7000-8000-000000000001")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[0] != 200 || codes[2] != 200 || codes[3] != http.StatusTooManyRequests || codes[4] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v", codes)
	}
}

// Nothing is keyed on the client address: a request without a session is not counted, and users
// behind one address are counted separately.
func TestRateLimitPerUserNeverKeysOnTheClientAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(signedIn)
	r.POST("/preview", middleware.RateLimitPerUser(1, time.Minute), func(c *gin.Context) { c.Status(http.StatusOK) })
	post := func(user string) int {
		req := httptest.NewRequest(http.MethodPost, "/preview", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		if user != "" {
			req.Header.Set("X-User", user)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 20; i++ {
		if post("") != http.StatusOK {
			t.Fatal("a request without a session must not be limited")
		}
	}
	a, b := "00000000-0000-7000-8000-00000000000a", "00000000-0000-7000-8000-00000000000b"
	if post(a) != http.StatusOK || post(b) != http.StatusOK {
		t.Fatal("each user has their own budget")
	}
	if post(a) != http.StatusTooManyRequests {
		t.Fatal("the second request of one user is over the limit of 1")
	}
}

// rateLimits is a limiter configuration with one refill rate per minute for every bucket.
func rateLimits(perMinute, userBurst, anonBurst, closedBurst int) config.RateLimitConfig {
	return config.RateLimitConfig{
		UserPerMinute:   perMinute,
		UserBurst:       userBurst,
		AnonPerMinute:   perMinute,
		AnonBurst:       anonBurst,
		ClosedPerMinute: perMinute,
		ClosedBurst:     closedBurst,
	}
}

func newLimiter(cfg config.RateLimitConfig, clock *time.Time) *middleware.RateLimiter {
	l := middleware.NewRateLimiter(cfg)
	middleware.SetRateLimiterClock(l, func() time.Time { return *clock })
	return l
}

// limiterRouter mounts the global middleware on real route templates and, in each handler, a stand-in for
// the permission gate (the user comes from X-User): /api/notifications/banners is a public read, /api/x needs a session,
// /api/events/upcoming has no gate.
func limiterRouter(l *middleware.RateLimiter) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(l.Anonymous)
	h := func(c *gin.Context) {
		if user := uuid.FromStringOrNil(c.GetHeader("X-User")); user != uuid.Nil {
			if !l.CheckUser(c, user) {
				return
			}
		} else if !l.CheckAnonymous(c, c.FullPath() == "/api/notifications/banners") {
			return
		}
		c.Status(http.StatusOK)
	}
	r.GET("/api/x", h)
	r.GET("/api/notifications/banners", h)
	r.GET("/api/health", h)
	r.GET("/api/events/:id/results/live", h)
	r.GET("/api/render/key", func(c *gin.Context) { c.Status(http.StatusOK) })
	// Two gates on one route: the second must not count the request again.
	r.GET("/api/twice", func(c *gin.Context) {
		h(c)
		if !c.IsAborted() {
			h(c)
		}
	})
	return r
}

func limGet(r *gin.Engine, path, remote, user string, cookie bool) *httptest.ResponseRecorder {
	return limServe(context.Background(), r, path, remote, user, cookie)
}

func limServe(
	ctx context.Context, r *gin.Engine, path, remote, user string, cookie bool,
) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	req.RemoteAddr = remote
	if user != "" {
		req.Header.Set("X-User", user)
	}
	if cookie {
		req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "x"})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// Anonymous requests share ONE public bucket whatever their address; the burst is spent, then 429 with
// Retry-After, and the bucket refills with time.
func TestRateLimiterAnonymousIsOneGlobalBucket(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(rateLimits(60, 5, 3, 3), &now)
	r := limiterRouter(l)
	for i := range 3 {
		remote := "203.0.113." + string(rune('1'+i)) + ":1"
		if w := limGet(r, "/api/notifications/banners", remote, "", false); w.Code != http.StatusOK {
			t.Fatalf("request %d from its own address: %d", i, w.Code)
		}
	}
	w := limGet(r, "/api/notifications/banners", "198.51.100.9:1", "", false)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("burst spent: code %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	now = now.Add(2 * time.Second)
	if w = limGet(r, "/api/notifications/banners", "198.51.100.9:1", "", false); w.Code != http.StatusOK {
		t.Fatalf("the bucket must refill with time, got %d", w.Code)
	}
}

// Signed-in users have a bucket each; one user's flood does not touch another's, and the same address
// is irrelevant.
func TestRateLimiterUsersHaveOwnBuckets(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(rateLimits(60, 2, 1, 1), &now)
	r := limiterRouter(l)
	a, b := "00000000-0000-7000-8000-00000000000a", "00000000-0000-7000-8000-00000000000b"
	for i := range 2 {
		if limGet(r, "/api/x", "203.0.113.5:1", a, true).Code != http.StatusOK {
			t.Fatalf("user a request %d refused", i)
		}
	}
	w := limGet(r, "/api/x", "203.0.113.5:1", a, true)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("user a over the burst: %d / %q", w.Code, w.Header().Get("Retry-After"))
	}
	if limGet(r, "/api/x", "203.0.113.5:1", b, true).Code != http.StatusOK {
		t.Fatal("user b has their own bucket")
	}
}

// Health checks and SSE connects are never counted.
func TestRateLimiterSkipsHealthAndStreams(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(rateLimits(1, 1, 1, 1), &now)
	r := limiterRouter(l)
	for i := range 20 {
		for _, path := range []string{"/api/health", "/api/events/1/results/live"} {
			if w := limGet(r, path, "203.0.113.5:1", "", false); w.Code != http.StatusOK {
				t.Fatalf("%s request %d: %d", path, i, w.Code)
			}
		}
	}
}

// Both limiters answer with the same 429: Retry-After (whole seconds, at least one) and the error envelope
// carrying the ErrAuthTooManyRequests code.
func TestLimitersShareOne429Format(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wantCode := authModel.ErrAuthTooManyRequests.WithDetail("x", 1).Err().StatusCode().FullCode()
	check := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusTooManyRequests {
			t.Fatalf("%s: status %d", name, w.Code)
		}
		if secs, err := strconv.Atoi(w.Header().Get("Retry-After")); err != nil || secs < 1 {
			t.Fatalf("%s: Retry-After %q", name, w.Header().Get("Retry-After"))
		}
		var body struct {
			Status struct {
				Code int `json:"Code"`
			} `json:"Status"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Status.Code != wantCode {
			t.Fatalf("%s: body %q (code %d, want %d)", name, w.Body.String(), body.Status.Code, wantCode)
		}
	}

	r := gin.New()
	r.Use(signedIn)
	r.POST("/preview", middleware.RateLimitPerUser(1, 30*time.Second), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	var w *httptest.ResponseRecorder
	for range 2 {
		req := httptest.NewRequest(http.MethodPost, "/preview", nil)
		req.Header.Set("X-User", "00000000-0000-7000-8000-000000000001")
		w = httptest.NewRecorder()
		r.ServeHTTP(w, req)
	}
	check("per-user", w)
	if ra, _ := strconv.Atoi(w.Header().Get("Retry-After")); ra > 30 {
		t.Fatalf("per-user Retry-After %d exceeds the window", ra)
	}

	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(rateLimits(60, 1, 1, 1), &now)
	lr := limiterRouter(l)
	limGet(lr, "/api/notifications/banners", "203.0.113.1:1", "", false)
	check("bucket", limGet(lr, "/api/notifications/banners", "203.0.113.1:1", "", false))
}

// Cookie-less requests to routes that need a session, and to paths no route matches, spend closed; the
// public readers keep their own budget, and the other way round.
func TestRateLimiterClosedAndPublicAreSeparateBuckets(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(config.RateLimitConfig{
		UserPerMinute:   60,
		UserBurst:       1,
		AnonPerMinute:   60,
		AnonBurst:       2,
		ClosedPerMinute: 60,
		ClosedBurst:     2,
	}, &now)
	r := limiterRouter(l)
	for i := range 2 {
		if w := limGet(r, "/api/x", "203.0.113.5:1", "", false); w.Code != http.StatusOK {
			t.Fatalf("closed request %d: %d", i, w.Code)
		}
	}
	if w := limGet(r, "/api/x", "203.0.113.5:1", "", false); w.Code != 429 {
		t.Fatalf("closed burst spent: %d", w.Code)
	}
	if w := limGet(r, "/api/no-such-route", "203.0.113.5:1", "", false); w.Code != 429 {
		t.Fatalf("an unmatched path is counted in closed: %d", w.Code)
	}
	for i := range 2 {
		if w := limGet(r, "/api/notifications/banners", "203.0.113.5:1", "", false); w.Code != http.StatusOK {
			t.Fatalf("a public read after the closed flood, request %d: %d", i, w.Code)
		}
	}
	if w := limGet(r, "/api/notifications/banners", "203.0.113.5:1", "", false); w.Code != 429 {
		t.Fatalf("public burst spent: %d", w.Code)
	}
}

func observed(l *middleware.RateLimiter) func() []middleware.Bucket {
	var seen []middleware.Bucket
	l.Observe(func(b middleware.Bucket) { seen = append(seen, b) })
	return func() []middleware.Bucket {
		out := seen
		seen = nil
		return out
	}
}

// Each request is counted once. Without a cookie the global middleware counts it: an ungated route and a
// public read in public, a route that needs a session and an unknown path in closed. With a cookie an unknown
// path and an ungated route are still counted there (no identity is ever established on them), a gated route
// is left to its gate, which counts the user once however many gates the route has.
func TestRateLimiterCountsEachRequestOnce(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(config.RateLimitConfig{
		UserPerMinute:   60,
		UserBurst:       10,
		AnonPerMinute:   60,
		AnonBurst:       10,
		ClosedPerMinute: 60,
		ClosedBurst:     10,
	}, &now)
	seen := observed(l)
	r := limiterRouter(l)
	user := "00000000-0000-7000-8000-00000000000a"
	for _, tc := range []struct {
		path   string
		cookie bool
		user   string
		want   []middleware.Bucket
	}{
		{"/api/render/key", false, "", []middleware.Bucket{middleware.BucketPublic}},
		{"/api/notifications/banners", false, "", []middleware.Bucket{middleware.BucketPublic}},
		{"/api/x", false, "", []middleware.Bucket{middleware.BucketClosed}},
		{"/api/twice", false, "", []middleware.Bucket{middleware.BucketClosed}},
		{"/api/no-such-route", false, "", []middleware.Bucket{middleware.BucketClosed}},
		{"/api/health", false, "", nil},
		{"/api/events/1/results/live", false, "", nil},
		{"/api/no-such-route", true, "", []middleware.Bucket{middleware.BucketClosed}},
		{"/api/render/key", true, "", []middleware.Bucket{middleware.BucketPublic}},
		{"/api/x", true, user, []middleware.Bucket{middleware.BucketUser}},
		{"/api/twice", true, user, []middleware.Bucket{middleware.BucketUser}},
	} {
		limGet(r, tc.path, "203.0.113.5:1", tc.user, tc.cookie)
		if got := seen(); fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s (cookie %v): counted in %v, want %v", tc.path, tc.cookie, got, tc.want)
		}
	}
}

// A request the gate already counted in its user's bucket is not counted again as anonymous (a silent route
// whose caller could not be loaded goes on anonymous).
func TestRateLimiterUserThenAnonymousCountedOnce(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(rateLimits(60, 10, 10, 10), &now)
	seen := observed(l)
	r := gin.New()
	r.Use(l.Anonymous)
	r.GET("/api/notifications/banners", func(c *gin.Context) {
		if l.CheckUser(c, uuid.FromStringOrNil("00000000-0000-7000-8000-00000000000a")) &&
			l.CheckAnonymous(c, true) {
			c.Status(http.StatusOK)
		}
	})
	limGet(r, "/api/notifications/banners", "203.0.113.5:1", "", true)
	if got := seen(); fmt.Sprint(got) != fmt.Sprint([]middleware.Bucket{middleware.BucketUser}) {
		t.Fatalf("counted in %v, want only the user bucket", got)
	}
}

// The internal listener (in-cluster SSR: one trusted caller in front of many visitors) is not counted in the
// anonymous buckets; the in-flight cap bounds it.
func TestRateLimiterSkipsAnonymousOnTheInternalListener(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(rateLimits(60, 1, 1, 1), &now)
	seen := observed(l)
	r := limiterRouter(l)
	internal := middleware.WithInternalListener(context.Background())
	paths := []string{"/api/notifications/banners", "/api/x", "/api/no-such-route", "/api/render/key"}
	for range 5 {
		for _, path := range paths {
			w := limServe(internal, r, path, "10.0.0.5:1", "", false)
			if w.Code == http.StatusTooManyRequests {
				t.Fatalf("%s on the internal listener was rate limited", path)
			}
		}
	}
	if got := seen(); len(got) != 0 {
		t.Fatalf("the internal listener was counted in %v", got)
	}
	user := "00000000-0000-7000-8000-00000000000a"
	limServe(internal, r, "/api/x", "10.0.0.5:1", user, true)
	if got := seen(); fmt.Sprint(got) != fmt.Sprint([]middleware.Bucket{middleware.BucketUser}) {
		t.Fatalf("a signed-in request on the internal listener: counted in %v", got)
	}
}
