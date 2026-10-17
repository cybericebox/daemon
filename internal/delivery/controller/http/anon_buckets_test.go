package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

// bucketUseCase is chainUseCase plus the visible banners, so GET /api/banners answers 200.
type bucketUseCase struct{ chainUseCase }

func (bucketUseCase) ListVisibleSiteBanners(
	context.Context,
	string,
	*uuid.UUID,
	*uuid.UUID,
) ([]siteBannerModel.Banner, error) {
	return nil, nil
}

const bucketOrigin = "https://admin.example.test"

// bucketController is the real NewController chain with the given limits; gin's text logger and panic dump
// (a nil use case behind most routes) and zerolog go nowhere.
func bucketController(t *testing.T, limits config.RateLimitConfig) (*Controller, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	prevOut, prevErr, prevLog := gin.DefaultWriter, gin.DefaultErrorWriter, log.Logger
	gin.DefaultWriter, gin.DefaultErrorWriter, log.Logger = io.Discard, io.Discard, zerolog.Nop()
	t.Cleanup(
		func() { gin.DefaultWriter, gin.DefaultErrorWriter, log.Logger = prevOut, prevErr, prevLog },
	)
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
		Inflight:  config.InflightConfig{},
	})
	router, ok := ctrl.server.listeners[0].srv.Handler.(*gin.Engine)
	if !ok {
		t.Fatal("the API listener does not serve the gin router")
	}
	return ctrl, router
}

// sendAnonymous sends a cookie-less request from an allowed page; a handler that waits on its context
// gives up after a second.
func sendAnonymous(router *gin.Engine, method, path string) *httptest.ResponseRecorder {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req := httptest.NewRequest(method, path, nil).WithContext(ctx)
	req.Host = "api.example.test"
	req.Header.Set("Origin", bucketOrigin)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
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

// Every mounted route, and a path no route matches, counts a cookie-less request exactly once: in public when
// a caller may use the route without a session (an ungated public route, or a GET gated by a permission
// RolePublic holds: middleware.PublicReadRoutes, compared with the router in the handler package), in closed
// otherwise; the exempt routes are not counted at all.
func TestEveryRouteCountedOnceWithoutCookie(t *testing.T) {
	big := 1 << 20
	ctrl, router := bucketController(t, config.RateLimitConfig{
		UserPerMinute:   big,
		UserBurst:       big,
		AnonPerMinute:   big,
		AnonBurst:       big,
		ClosedPerMinute: big,
		ClosedBurst:     big,
	})
	var (
		mu   sync.Mutex
		seen []middleware.Bucket
	)
	ctrl.limiter.Observe(func(b middleware.Bucket) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, b)
	})
	exempt := map[string]bool{}
	for _, path := range middleware.RateLimitExemptRoutes() {
		exempt[path] = true
	}
	ungated := map[string]bool{}
	for _, route := range middleware.UngatedPublicRoutes() {
		ungated[route] = true
	}
	routes := append(
		router.Routes(),
		gin.RouteInfo{Method: http.MethodGet, Path: "/api/no-such-route"},
	)
	if len(routes) < 100 {
		t.Fatalf("only %d routes mounted: the handler tree is missing", len(routes))
	}
	for _, route := range routes {
		var want []middleware.Bucket
		switch {
		case exempt[route.Path]:
		case ungated[route.Method+" "+route.Path] || route.Method == http.MethodGet && middleware.PublicReadRoutes[route.Path]:
			want = []middleware.Bucket{middleware.BucketPublic}
		default:
			want = []middleware.Bucket{middleware.BucketClosed}
		}
		mu.Lock()
		seen = nil
		mu.Unlock()
		w := sendAnonymous(router, route.Method, filledPath(route.Path))
		mu.Lock()
		got := append([]middleware.Bucket(nil), seen...)
		mu.Unlock()
		if len(got) != len(want) || len(got) == 1 && got[0] != want[0] {
			t.Errorf(
				"%s %s (status %d): counted in %v, want %v",
				route.Method,
				route.Path,
				w.Code,
				got,
				want,
			)
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
	})
	for i := range 3 {
		if w := sendAnonymous(
			router,
			http.MethodGet,
			"/api/auth/me",
		); w.Code != http.StatusUnauthorized {
			t.Fatalf("closed request %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	for _, path := range []string{"/api/auth/me", "/api/no-such-route"} {
		if w := sendAnonymous(router, http.MethodGet, path); w.Code != http.StatusTooManyRequests {
			t.Fatalf("%s after the closed burst: %d", path, w.Code)
		}
	}
	w := sendAnonymous(router, http.MethodGet, "/api/banners")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/banners after the closed flood: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != bucketOrigin {
		t.Fatalf("CORS must run before the limiter: Access-Control-Allow-Origin %q", got)
	}
}
