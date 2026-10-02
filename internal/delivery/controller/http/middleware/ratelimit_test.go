package middleware_test

import (
	"net/http"
	"net/http/httptest"
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

func newLimiter(cfg config.RateLimitConfig, clock *time.Time) *middleware.RateLimiter {
	l := middleware.NewRateLimiter(cfg)
	middleware.SetRateLimiterClock(l, func() time.Time { return *clock })
	return l
}

func limiterRouter(l *middleware.RateLimiter, userCheck bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(l.Anonymous)
	h := func(c *gin.Context) {
		if userCheck {
			claims := uuid.FromStringOrNil(c.GetHeader("X-User"))
			if !l.Check(c, claims, claims != uuid.Nil) {
				return
			}
		}
		c.Status(http.StatusOK)
	}
	r.GET("/api/x", h)
	r.GET("/api/health", h)
	r.GET("/api/events/:id/results/live", h)
	return r
}

func limGet(r *gin.Engine, path, remote, user string, cookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
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

// Anonymous requests share ONE bucket whatever their address; the burst is spent, then 429 with Retry-After,
// and the bucket refills with time.
func TestRateLimiterAnonymousIsOneGlobalBucket(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(config.RateLimitConfig{UserPerMinute: 60, UserBurst: 5, AnonPerMinute: 60, AnonBurst: 3}, &now)
	r := limiterRouter(l, false)
	for i := 0; i < 3; i++ {
		if w := limGet(r, "/api/x", "203.0.113."+string(rune('1'+i))+":1", "", false); w.Code != http.StatusOK {
			t.Fatalf("request %d from its own address: %d", i, w.Code)
		}
	}
	w := limGet(r, "/api/x", "198.51.100.9:1", "", false)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("burst spent: code %d, Retry-After %q", w.Code, w.Header().Get("Retry-After"))
	}
	now = now.Add(2 * time.Second)
	if w = limGet(r, "/api/x", "198.51.100.9:1", "", false); w.Code != http.StatusOK {
		t.Fatalf("the bucket must refill with time, got %d", w.Code)
	}
}

// Signed-in users have a bucket each; one user's flood does not touch another's, and the same address
// is irrelevant.
func TestRateLimiterUsersHaveOwnBuckets(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(config.RateLimitConfig{UserPerMinute: 60, UserBurst: 2, AnonPerMinute: 60, AnonBurst: 1}, &now)
	r := limiterRouter(l, true)
	a, b := "00000000-0000-7000-8000-00000000000a", "00000000-0000-7000-8000-00000000000b"
	for i := 0; i < 2; i++ {
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
	l := newLimiter(config.RateLimitConfig{UserPerMinute: 1, UserBurst: 1, AnonPerMinute: 1, AnonBurst: 1}, &now)
	r := limiterRouter(l, true)
	for i := 0; i < 20; i++ {
		for _, path := range []string{"/api/health", "/api/events/1/results/live"} {
			if w := limGet(r, path, "203.0.113.5:1", "", false); w.Code != http.StatusOK {
				t.Fatalf("%s request %d: %d", path, i, w.Code)
			}
		}
	}
}

// A refused request names its limiter for the error journal.
func TestRateLimiterRecordsTheLimiterName(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	l := newLimiter(config.RateLimitConfig{UserPerMinute: 60, UserBurst: 1, AnonPerMinute: 60, AnonBurst: 1}, &now)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var names []string
	r.Use(func(c *gin.Context) {
		c.Next()
		if v, ok := c.Get("errjournal.limiter"); ok {
			names = append(names, v.(string))
		}
	}, l.Anonymous)
	r.GET("/api/x", func(c *gin.Context) { c.Status(http.StatusOK) })
	limGet(r, "/api/x", "203.0.113.5:1", "", false)
	limGet(r, "/api/x", "203.0.113.5:1", "", false)
	if len(names) != 1 || names[0] != "anonymous" {
		t.Fatalf("journal limiter names = %v", names)
	}
}
