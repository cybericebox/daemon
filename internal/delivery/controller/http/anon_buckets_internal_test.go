package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
	pulseUseCase "github.com/cybericebox/daemon/internal/useCase/notification/pulse"
)

// bucketUseCase is chainUseCase plus the banner list, so GET /api/notifications/banners answers 200.
type bucketUseCase struct{ chainUseCase }

func (bucketUseCase) Banners(context.Context, siteModel.Site, pulseUseCase.Viewer) (pulseUseCase.BannerList, error) {
	return pulseUseCase.BannerList{}, nil
}

const bucketOrigin = "https://admin.example.test"

// bucketController is the real NewController chain with the given limits. It runs in release mode with
// zerolog off, so neither the request lines nor the panic dumps (a nil use case behind most routes) are
// printed.
func bucketController(
	t *testing.T, limits config.RateLimitConfig, inflight config.InflightConfig,
) (*Controller, *gin.Engine) {
	t.Helper()
	prevLevel := zerolog.GlobalLevel()
	gin.SetMode(gin.ReleaseMode)
	zerolog.SetGlobalLevel(zerolog.Disabled)
	t.Cleanup(func() {
		gin.SetMode(gin.TestMode)
		zerolog.SetGlobalLevel(prevLevel)
	})
	ctrl := NewController(Dependencies{
		UseCase: bucketUseCase{},
		Config: &config.HTTPControllerConfig{
			MaxBodyBytes: 1 << 20,
			Server:       config.HTTPServerConfig{Host: "127.0.0.1", Port: "0"},
		},
		AuthConfig: config.AuthConfig{
			SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			Hosts: config.HostsConfig{
				Main:        "example.test",
				API:         "api.example.test",
				ID:          "id.example.test",
				Admin:       "admin.example.test",
				Exercises:   "exercises.example.test",
				EventDomain: "example.test",
			},
			Captcha: config.CaptchaConfig{Provider: config.CaptchaNone},
		},
		RateLimit: limits,
		Inflight:  inflight,
	})
	router, ok := ctrl.server.listeners[0].srv.Handler.(*gin.Engine)
	if !ok {
		t.Fatal("the API listener does not serve the gin router")
	}
	return ctrl, router
}

// sendFromPage sends a request from an allowed page, with the session cookie when cookie is not empty; a
// handler that waits on its context gives up after a second.
func sendFromPage(
	router *gin.Engine, method, path, cookie string, body io.Reader,
) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := httptest.NewRequest(method, path, body).WithContext(ctx)
	req.Host = "api.example.test"
	req.Header.Set("Origin", bucketOrigin)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: cookie})
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

// getAnonymous is a cookie-less GET from an allowed page.
func getAnonymous(router *gin.Engine, path string) *httptest.ResponseRecorder {
	return sendFromPage(router, http.MethodGet, path, "", nil)
}

// filledPath puts dummy values into a route template's parameters.
func filledPath(template string) string {
	parts := strings.Split(template, "/")
	for i, part := range parts {
		switch {
		case strings.HasPrefix(part, ":"):
			parts[i] = "00000000-0000-7000-8000-000000000001"
		case strings.HasPrefix(part, "*"):
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}

// wantBucket is where a request without identity belongs: public when a caller may use the route without a
// session (an ungated route, or a GET gated by a permission RolePublic holds: middleware.PublicReadRoutes,
// compared with the router's gates in the handler package), closed otherwise; nowhere on an exempt route.
func wantBucket(method, path string) []middleware.Bucket {
	for _, exempt := range middleware.RateLimitExemptRoutes() {
		if exempt == path {
			return nil
		}
	}
	for _, ungated := range middleware.UngatedPublicRoutes() {
		if ungated == method+" "+path {
			return []middleware.Bucket{middleware.BucketPublic}
		}
	}
	if method == http.MethodGet && middleware.PublicReadRoutes[path] {
		return []middleware.Bucket{middleware.BucketPublic}
	}
	return []middleware.Bucket{middleware.BucketClosed}
}

// Every mounted route, and a path no route matches, counts a request without identity exactly once, in the
// bucket of the route: without a cookie, and with a cookie that does not open.
func TestEveryRouteCountedOnceWithoutIdentity(t *testing.T) {
	big := 1 << 20
	ctrl, router := bucketController(t, config.RateLimitConfig{
		UserPerMinute:   big,
		UserBurst:       big,
		AnonPerMinute:   big,
		AnonBurst:       big,
		ClosedPerMinute: big,
		ClosedBurst:     big,
	}, config.InflightConfig{})
	var (
		mu   sync.Mutex
		seen []middleware.Bucket
	)
	ctrl.limiter.Observe(func(b middleware.Bucket) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, b)
	})
	routes := append(
		router.Routes(),
		gin.RouteInfo{Method: http.MethodGet, Path: "/api/no-such-route"},
	)
	if len(routes) < 100 {
		t.Fatalf("only %d routes mounted: the handler tree is missing", len(routes))
	}
	for _, cookie := range []string{"", "garbage"} {
		for _, route := range routes {
			want := wantBucket(route.Method, route.Path)
			mu.Lock()
			seen = nil
			mu.Unlock()
			w := sendFromPage(router, route.Method, filledPath(route.Path), cookie, nil)
			mu.Lock()
			got := append([]middleware.Bucket(nil), seen...)
			mu.Unlock()
			if len(got) != len(want) || len(got) == 1 && got[0] != want[0] {
				t.Errorf("%s %s (cookie %q, status %d): counted in %v, want %v",
					route.Method, route.Path, cookie, w.Code, got, want)
			}
		}
	}
}

// A flood of cookie-less requests to routes that need a session (401s) and to unknown paths (404s) spends
// closed only: public readers are still served.
func TestClosedFloodDoesNotStarvePublic(t *testing.T) {
	_, router := bucketController(t, config.RateLimitConfig{
		UserPerMinute:   60,
		UserBurst:       10,
		AnonPerMinute:   1,
		AnonBurst:       5,
		ClosedPerMinute: 1,
		ClosedBurst:     3,
	}, config.InflightConfig{})
	for i := range 3 {
		w := getAnonymous(router, "/api/auth/me")
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("closed request %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/auth/me", "/api/no-such-route"} {
		if w := getAnonymous(router, path); w.Code != http.StatusTooManyRequests {
			t.Fatalf("%s after the closed burst: %d", path, w.Code)
		}
	}
	w := getAnonymous(router, "/api/notifications/banners?site=main")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/notifications/banners after the closed flood: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != bucketOrigin {
		t.Fatalf("CORS must run before the limiter: Access-Control-Allow-Origin %q", got)
	}
}

// readCounter counts the reads of a request body.
type readCounter struct {
	io.Reader

	reads atomic.Int32
}

func (r *readCounter) Read(p []byte) (int, error) {
	r.reads.Add(1)
	return r.Reader.Read(p)
}

// A request the rate limiter refuses never reaches the in-flight limiter: with the closed bucket empty and
// the anonymous in-flight share held, a cookie-less POST to a route that needs a session gets the 429 (not
// the overload 503) and its body is never read.
func TestRateRefusedRequestTakesNoInflightSlot(t *testing.T) {
	_, router := bucketController(t, config.RateLimitConfig{
		UserPerMinute:   60,
		UserBurst:       10,
		AnonPerMinute:   60,
		AnonBurst:       10,
		ClosedPerMinute: 1,
		ClosedBurst:     1,
	}, config.InflightConfig{Max: 4, AnonShare: 25})
	entered, release := make(chan struct{}, 1), make(chan struct{})
	router.GET("/api/test/block", func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		c.Status(http.StatusNoContent)
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		getAnonymous(router, "/api/test/block")
	}()
	t.Cleanup(func() { close(release); <-done })
	select {
	case <-entered:
	case <-done:
		t.Fatal("the blocking request did not reach its handler")
	case <-time.After(5 * time.Second):
		t.Fatal("the blocking request never reached its handler")
	}

	body := &readCounter{Reader: strings.NewReader(`{"Email":"a@b.test"}`)}
	w := sendFromPage(router, http.MethodPost, "/api/auth/account/email", "", body)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("want the 429 of the empty closed bucket, got %d %s", w.Code, w.Body.String())
	}
	if n := body.reads.Load(); n != 0 {
		t.Fatalf("the refused request's body was read %d times", n)
	}
}
