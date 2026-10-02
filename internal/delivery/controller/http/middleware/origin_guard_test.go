package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

func guardRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	hosts := config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test",
		Exercises: "exercises.example.test", EventDomain: "example.test", LabsDomain: "labs.example.test"}
	r := gin.New()
	r.Use(middleware.OriginGuard(hosts))
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	r.GET("/x", ok)
	r.POST("/x", ok)
	r.PUT("/x", ok)
	r.PATCH("/x", ok)
	r.DELETE("/x", ok)
	return r
}

func send(r *gin.Engine, method, body string, headers map[string]string) int {
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

// R-18: pages under the labs domain are same-site with the platform, so SameSite=Strict does not
// keep them out; their writes are refused by origin.
func TestOriginGuard_RefusesTheLabsDomain(t *testing.T) {
	r := guardRouter()
	json := "application/json"
	for _, origin := range []string{"https://web-abc123.labs.example.test", "https://labs.example.test", "https://A.B.LABS.example.test"} {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			if got := send(r, method, `{}`, map[string]string{"Origin": origin, "Content-Type": json}); got != http.StatusForbidden {
				t.Errorf("%s from %s: %d, want 403", method, origin, got)
			}
		}
	}
	// no Origin (a navigation or form post): the Referer names the source
	if got := send(r, "POST", `{}`, map[string]string{"Referer": "https://web-abc.labs.example.test/page", "Content-Type": json}); got != http.StatusForbidden {
		t.Errorf("Referer from the labs domain: %d, want 403", got)
	}
	// a lookalike host is not the labs domain
	if got := send(r, "POST", `{}`, map[string]string{"Origin": "https://evillabs.example.test", "Content-Type": json}); got != http.StatusOK {
		t.Errorf("lookalike: %d (the CORS gate, not this one, handles foreign hosts)", got)
	}
}

func TestOriginGuard_PlatformFrontendsPass(t *testing.T) {
	r := guardRouter()
	for _, origin := range []string{"https://id.example.test", "https://ctf.example.test", "https://admin.example.test"} {
		if got := send(r, "POST", `{"a":1}`, map[string]string{"Origin": origin, "Content-Type": "application/json; charset=utf-8"}); got != http.StatusOK {
			t.Errorf("%s: %d", origin, got)
		}
	}
	if got := send(r, "DELETE", ``, map[string]string{"Origin": "https://id.example.test"}); got != http.StatusOK {
		t.Errorf("a bodiless DELETE needs no content type: %d", got)
	}
	if got := send(r, "POST", `{}`, map[string]string{"Referer": "https://id.example.test/profile", "Content-Type": "application/json"}); got != http.StatusOK {
		t.Errorf("Referer from a frontend when Origin is absent: %d", got)
	}
}

func TestOriginGuard_WriteWithoutOriginOrRefererIsRefused(t *testing.T) {
	r := guardRouter()
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		if got := send(r, method, `{}`, map[string]string{"Content-Type": "application/json"}); got != http.StatusForbidden {
			t.Errorf("%s with neither Origin nor Referer: %d, want 403", method, got)
		}
	}
	if got := send(r, "POST", `{}`, map[string]string{"Origin": "null", "Content-Type": "application/json"}); got != http.StatusOK {
		// a "null" origin names a source; the CORS gate (an earlier middleware) rejects it
		t.Errorf("null origin is the CORS gate's: %d", got)
	}
}

func TestOriginGuard_SafeMethodsAreUntouched(t *testing.T) {
	r := guardRouter()
	if got := send(r, "GET", ``, nil); got != http.StatusOK {
		t.Errorf("GET without Origin: %d", got)
	}
	if got := send(r, "GET", ``, map[string]string{"Origin": "https://web.labs.example.test"}); got != http.StatusOK {
		t.Errorf("GET from the labs domain is a read (the CORS gate decides what the page may read): %d", got)
	}
}

// A cross-origin form post cannot send JSON: a body in any other type never reaches a JSON handler.
func TestOriginGuard_BodyMustBeJSONOrMultipart(t *testing.T) {
	r := guardRouter()
	origin := map[string]string{"Origin": "https://id.example.test"}
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "", "application/jsonx", "text/html"} {
		h := map[string]string{"Origin": origin["Origin"]}
		if ct != "" {
			h["Content-Type"] = ct
		}
		if got := send(r, "POST", `{"a":1}`, h); got != http.StatusUnsupportedMediaType {
			t.Errorf("content type %q: %d, want 415", ct, got)
		}
	}
	if got := send(r, "POST", "--b\r\n--b--", map[string]string{"Origin": origin["Origin"], "Content-Type": "multipart/form-data; boundary=b"}); got != http.StatusOK {
		t.Errorf("an upload is multipart: %d", got)
	}
}
