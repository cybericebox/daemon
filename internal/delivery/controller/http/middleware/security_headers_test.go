package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

func headersRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	hosts := config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test",
		Exercises: "exercises.example.test", EventDomain: "events.example.test"}
	r := gin.New()
	r.Use(middleware.SecurityHeaders, middleware.HandleCORSMiddleWare(hosts), middleware.OriginGuard(hosts))
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/data", ok)
	r.GET("/image", middleware.SameSiteResource, ok)
	r.GET("/api/auth/avatar/:id", middleware.PublicMedia, ok)
	r.POST("/api/auth/avatar/:id", ok)
	return r
}

func get(r *gin.Engine, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// A script on a lab page must not even load API data: no-cors loads (<img>, <script>, <link>) from
// another origin are blocked by the browser, and no page may frame the API.
func TestSecurityHeaders_BlockNoCorsLoadsAndFraming(t *testing.T) {
	r := headersRouter()
	w := get(r, "/data", nil)
	for header, want := range map[string]string{
		"Cross-Origin-Resource-Policy": "same-origin",
		"Content-Security-Policy":      "frame-ancestors 'none'",
		"X-Frame-Options":              "DENY",
		"X-Content-Type-Options":       "nosniff",
		"Referrer-Policy":              "no-referrer",
	} {
		if got := w.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// CORS fetches from our frontends keep working: the same response that carries CORP: same-origin
// carries the CORS grant (a request that passes CORS is not subject to CORP), and a refused
// origin still gets no grant.
func TestSecurityHeaders_DoNotBreakCorsFromTheFrontends(t *testing.T) {
	r := headersRouter()
	for _, origin := range []string{"https://id.example.test", "https://ctf.events.example.test", "https://admin.example.test"} {
		w := get(r, "/data", map[string]string{"Origin": origin})
		if w.Code != http.StatusOK || w.Header().Get("Access-Control-Allow-Origin") != origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
			t.Errorf("%s: %d ACAO=%q", origin, w.Code, w.Header().Get("Access-Control-Allow-Origin"))
		}
	}
	w := get(r, "/data", map[string]string{"Origin": "https://web-x.labs.example.test"})
	if w.Code != http.StatusForbidden || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("a lab page: %d ACAO=%q", w.Code, w.Header().Get("Access-Control-Allow-Origin"))
	}
	// preflight
	req := httptest.NewRequest(http.MethodOptions, "/data", nil)
	req.Header.Set("Origin", "https://id.example.test")
	pre := httptest.NewRecorder()
	r.ServeHTTP(pre, req)
	if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Origin") == "" {
		t.Errorf("preflight: %d", pre.Code)
	}
}

// The few URLs the frontends load as <img src> / <link rel=icon> from another subdomain relax it to same-site.
func TestSameSiteResource_RelaxesOnlyTheRoutesItIsOn(t *testing.T) {
	r := headersRouter()
	if got := get(r, "/image", nil).Header().Get("Cross-Origin-Resource-Policy"); got != "same-site" {
		t.Errorf("image route: %q", got)
	}
	if got := get(r, "/data", nil).Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("data route: %q", got)
	}
	// an <img> sends no Origin on a GET: it passes the guard, and the browser then applies the CORP
	if w := get(r, "/image", nil); w.Code != http.StatusOK {
		t.Errorf("a nameless image GET: %d", w.Code)
	}
}

// Public, unauthenticated media that emails, external pages and link previews embed: any site may load
// it (CORP cross-origin) and its Referer is whoever embeds it, so it is not checked. Everything else
// keeps same-origin and the Referer rule. An Origin, when present, is still checked.
func TestPublicMedia_CrossOriginAndNoRefererCheck(t *testing.T) {
	r := headersRouter()
	const path = "/api/auth/avatar/x"
	w := get(r, path, map[string]string{"Referer": "https://mail.google.com/mail/u/0/"})
	if w.Code != http.StatusOK || w.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("an external embedder: %d CORP=%q", w.Code, w.Header().Get("Cross-Origin-Resource-Policy"))
	}
	if w := get(r, path, nil); w.Code != http.StatusOK {
		t.Errorf("no Referer: %d", w.Code)
	}
	// the Origin check is not relaxed
	for _, origin := range []string{"https://evil.test", "null", "https://web-x.labs.example.test"} {
		if w := get(r, path, map[string]string{"Origin": origin}); w.Code != http.StatusForbidden {
			t.Errorf("Origin %s on public media: %d, want 403", origin, w.Code)
		}
	}
	// the exemption is for reads of those routes only
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.Header.Set("Referer", "https://mail.google.com/")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("a write is never exempt: %d", rec.Code)
	}
	// any other route keeps the Referer rule and same-origin
	other := get(r, "/data", map[string]string{"Referer": "https://mail.google.com/mail/u/0/"})
	if other.Code != http.StatusForbidden {
		t.Errorf("a non-media route with a foreign Referer: %d, want 403", other.Code)
	}
	if got := get(r, "/data", nil).Header().Get("Cross-Origin-Resource-Policy"); got != "same-origin" {
		t.Errorf("other routes: CORP %q", got)
	}
}
