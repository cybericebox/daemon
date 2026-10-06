package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

func newRouter(domain string) *gin.Engine {
	hosts := config.HostsConfig{Main: domain, API: "api." + domain, ID: "id." + domain, Admin: "admin." + domain, Exercises: "exercises." + domain, EventDomain: domain}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.HandleCORSMiddleWare(hosts))
	r.GET("/api/auth/me", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/auth/sign-out", func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func TestHandle_NoOrigin_PassesThrough(t *testing.T) {
	r := newRouter("example.test")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("Access-Control-Allow-Origin should be unset for a no-Origin request")
	}
}

func TestHandle_AllowedApexOrigin(t *testing.T) {
	r := newRouter("example.test")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Origin", "https://example.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.test" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want the echoed apex origin", got)
	}
	if w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("Access-Control-Allow-Credentials must be true")
	}
	if w.Header().Get("Vary") != "Origin" {
		t.Fatalf("Vary: Origin must be set")
	}
}

func TestHandle_AllowedSubdomainOrigin(t *testing.T) {
	for _, sub := range []string{"id", "admin", "event-tag-1"} {
		r := newRouter("example.test")
		req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
		req.Header.Set("Origin", "https://"+sub+".example.test")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if got := w.Header().
			Get("Access-Control-Allow-Origin"); got != "https://"+sub+".example.test" {
			t.Fatalf("subdomain %q: Access-Control-Allow-Origin = %q, want it echoed", sub, got)
		}
	}
}

func TestHandle_RejectsOffPlatformOrigin(t *testing.T) {
	r := newRouter("example.test")
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-out", nil)
	req.Header.Set("Origin", "https://evil-example.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for an off-platform Origin", w.Code)
	}
}

func TestHandle_RejectsHTTPOrigin(t *testing.T) {
	r := newRouter("example.test")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Origin", "http://example.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for a non-https Origin", w.Code)
	}
}

func TestHandle_Preflight(t *testing.T) {
	r := newRouter("example.test")
	req := httptest.NewRequest(http.MethodOptions, "/api/auth/sign-out", nil)
	req.Header.Set("Origin", "https://id.example.test")
	req.Header.Set("Access-Control-Request-Method", "POST")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Methods"); got == "" {
		t.Fatalf("Access-Control-Allow-Methods must be set on preflight")
	}
	if got := w.Header().Get("Access-Control-Max-Age"); got == "" {
		t.Fatalf("Access-Control-Max-Age must be set on preflight")
	}
}

// TestHandle_ExposesSignInURLAndContentDisposition: cross-origin JS can only
// read response headers listed here — the sign-in redirect URL and the
// export archive filename (Content-Disposition).
func TestHandle_ExposesSignInURLAndContentDisposition(t *testing.T) {
	r := newRouter("example.test")
	req := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
	req.Header.Set("Origin", "https://admin.example.test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if got := w.Header().Get("Access-Control-Expose-Headers"); got != "X-Sign-In-URL, Content-Disposition, Retry-After, X-Request-ID" {
		t.Fatalf("Access-Control-Expose-Headers = %q, want %q", got, "X-Sign-In-URL, Content-Disposition, Retry-After, X-Request-ID")
	}
}

type tagSet map[string]bool

func (s tagSet) EventTagExists(_ context.Context, tag string) bool { return s[tag] }

// An event site whose event is gone is a missing tenant (404, like the tenant resolver says), a foreign
// origin stays 403, and an existing event passes on to the handler.
func TestHandleCORS_MissingEventSiteIs404NotForbidden(t *testing.T) {
	hosts := config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "events.example.test"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.HandleCORS(middleware.OriginPolicy{Hosts: hosts, Tags: tagSet{"alive1": true}}))
	r.GET("/api/events/self/public-info", func(c *gin.Context) { c.Status(http.StatusOK) })
	for origin, want := range map[string]int{
		"https://alive1.events.example.test": http.StatusOK,
		"https://gone22.events.example.test": http.StatusNotFound,
		"https://evil.test":                  http.StatusForbidden,
	} {
		req := httptest.NewRequest(http.MethodGet, "/api/events/self/public-info", nil)
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != want {
			t.Errorf("origin %s: status = %d, want %d", origin, w.Code, want)
		}
	}
}

// An unknown or deleted event site is answered 404 WITH the CORS headers (so the browser lets the frontend
// read it), its preflight is a normal one, and a foreign origin still gets neither.
func TestHandleCORS_MissingEventSiteIsReadable(t *testing.T) {
	hosts := config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "events.example.test"}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.HandleCORS(middleware.OriginPolicy{Hosts: hosts, Tags: tagSet{"alive1": true, "deleted1": false}}))
	r.GET("/api/events/self/public-info", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/api/auth/me", func(c *gin.Context) { c.Status(http.StatusOK) })
	for _, origin := range []string{"https://nosuchevent.events.example.test", "https://deleted1.events.example.test"} {
		pre := httptest.NewRequest(http.MethodOptions, "/api/events/self/public-info", nil)
		pre.Header.Set("Origin", origin)
		pre.Header.Set("Access-Control-Request-Method", "GET")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, pre)
		if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Methods") == "" {
			t.Errorf("%s preflight: status %d, headers %v", origin, w.Code, w.Header())
		}
		for _, path := range []string{"/api/events/self/public-info", "/api/auth/me"} {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Origin", origin)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound || w.Header().Get("Access-Control-Allow-Origin") != origin ||
				w.Header().Get("Access-Control-Allow-Credentials") != "true" || w.Header().Get("Vary") != "Origin" {
				t.Errorf("%s %s: status %d, headers %v", origin, path, w.Code, w.Header())
			}
		}
	}
	for _, method := range []string{http.MethodOptions, http.MethodGet} {
		req := httptest.NewRequest(method, "/api/events/self/public-info", nil)
		req.Header.Set("Origin", "https://evil.test")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("foreign %s: status %d, headers %v", method, w.Code, w.Header())
		}
	}
}
