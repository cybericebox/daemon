package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	authUC "github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type renderGarbageSession struct {
	protection.IUseCase
	opens atomic.Int32
}

func (f *renderGarbageSession) OpenSession(string) (authUC.SessionPass, error) {
	f.opens.Add(1)
	return authUC.SessionPass{}, authModel.ErrAuthInvalidSession.Err()
}

type renderAdmissionLimiter struct {
	*middleware.RateLimiter
	checks atomic.Int32
}

func (l *renderAdmissionLimiter) CheckAnonymous(ctx *gin.Context, public bool) bool {
	if !public {
		l.checks.Add(1)
	}
	return l.RateLimiter.CheckAnonymous(ctx, public)
}

// The real global limiter skips nonempty cookies, and hard authentication
// counts the forged cookie in the closed bucket before refusing it. Admission must still bound the waits.
func TestRenderAdmissionBoundsForgedCookieConcurrencyAndReleasesCancellation(t *testing.T) {
	const limit = 128
	gin.SetMode(gin.TestMode)
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", API: "api.example.test", ID: "id.example.test", EventDomain: "example.test"}}
	auth := &renderGarbageSession{}
	limits := config.RateLimitConfig{
		AnonBurst:       1,
		AnonPerMinute:   1,
		UserBurst:       1,
		UserPerMinute:   1,
		ClosedBurst:     1000,
		ClosedPerMinute: 1,
	}
	limiter := &renderAdmissionLimiter{RateLimiter: middleware.NewRateLimiter(limits)}
	prot := protection.New(protection.Dependencies{UseCase: auth, Config: cfg, Limiter: limiter})
	h := NewEventRenderAPIHandler(&renderFake{}, prot, cfg)
	h.now = func() time.Time { return time.Unix(1800000000, 0) }
	entered := make(chan struct{}, limit+2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	h.wait = func(ctx context.Context, _ time.Duration) error {
		entered <- struct{}{}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	policy := middleware.OriginPolicy{Hosts: cfg.Hosts, Tags: &renderTagProbe{}}
	router := gin.New()
	router.Use(response.WithErrorHandler, prot.RequireAPIHost, middleware.HandleCORS(policy), middleware.OriginGuardWith(policy), limiter.Anonymous)
	h.Init(router.Group("api"))
	request := func(ctx context.Context, tag string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/events/self/render-grant", strings.NewReader(`{"Challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`)).WithContext(ctx)
		req.Host = cfg.Hosts.API
		req.Header.Set("Origin", "https://"+tag+".example.test")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "garbage"})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	t.Cleanup(func() { close(release); wg.Wait() })
	var cancelFirst context.CancelFunc
	var firstDone chan struct{}
	for i := 0; i < limit; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		if i == 0 {
			cancelFirst = cancel
			firstDone = done
		} else {
			defer cancel()
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer close(done); request(ctx, "summer") }()
	}
	for i := 0; i < limit; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("admitted requests did not reach controlled wait")
		}
	}
	require.Equal(t, int32(limit), auth.opens.Load())
	require.Equal(t, int32(limit), limiter.checks.Load(), "each forged cookie counts in closed")
	var overloaded []*httptest.ResponseRecorder
	for _, tag := range []string{"summer", "unknown"} {
		done := make(chan *httptest.ResponseRecorder, 1)
		wg.Add(1)
		go func() { defer wg.Done(); done <- request(context.Background(), tag) }()
		select {
		case rec := <-done:
			require.Equal(t, 429, rec.Code)
			overloaded = append(overloaded, rec)
		case <-entered:
			t.Fatal("oversubscribed request entered the deliberate denial wait")
		case <-time.After(5 * time.Second):
			t.Fatal("overload was queued rather than refused")
		}
	}
	// All four endpoints share this same bound, irrespective of cookie contents.
	for _, path := range []string{"/api/auth/event-render-key", "/api/events/self/render-manifest", "/api/events/self/render-page?revision=r1"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = cfg.Hosts.API
		req.Header.Set("Origin", "https://unknown.example.test")
		req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "garbage"})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 429, rec.Code)
		require.Equal(t, "1", rec.Header().Get("Retry-After"))
	}
	require.Equal(t, overloaded[0].Body.String(), overloaded[1].Body.String())
	for _, header := range []string{"Cache-Control", "Vary", "Content-Type", "Retry-After"} {
		require.Equal(t, overloaded[0].Header().Get(header), overloaded[1].Header().Get(header), header)
	}
	require.Equal(t, "1", overloaded[0].Header().Get("Retry-After"))
	require.Equal(t, "private, no-store", overloaded[0].Header().Get("Cache-Control"))
	require.Equal(t, int32(limit), auth.opens.Load(), "overload must precede authentication")
	cancelFirst()
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled wait did not release")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	wg.Add(1)
	go func() { defer wg.Done(); defer close(done); request(ctx, "unknown") }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled request did not release admission slot")
	}
	require.Equal(t, int32(limit+1), auth.opens.Load())
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement cancellation did not release")
	}
}

type renderAdmissionGate struct{ calls atomic.Int32 }

func (g *renderAdmissionGate) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { g.calls.Add(1); ctx.Next() }
}

func TestRenderAdmissionSuccessReleasesWithoutDenialDelay(t *testing.T) {
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	gate := &renderAdmissionGate{}
	h := NewEventRenderAPIHandler(&renderFake{public: true}, gate, cfg)
	h.wait = func(context.Context, time.Duration) error { t.Fatal("success paid denial delay"); return nil }
	r := gin.New()
	h.Init(r.Group("api"))
	for i := 0; i < 256; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/event-render-key", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code, "slot leaked at request %d", i)
	}
	// Canceled requests must never enter authentication or tenant-dependent work.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/auth/event-render-key", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, int32(256), gate.calls.Load(), "already canceled request entered authentication")
}
