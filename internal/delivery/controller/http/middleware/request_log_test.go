package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// captureLogs points gin's text writers and zerolog at buffers for one test.
func captureLogs(t *testing.T) (ginOut, zlOut *bytes.Buffer) {
	t.Helper()
	ginOut, zlOut = &bytes.Buffer{}, &bytes.Buffer{}
	prevOut, prevErr, prevLog, prevLevel := gin.DefaultWriter, gin.DefaultErrorWriter, log.Logger, zerolog.GlobalLevel()
	gin.DefaultWriter, gin.DefaultErrorWriter = ginOut, ginOut
	log.Logger = zerolog.New(zlOut)
	zerolog.SetGlobalLevel(zerolog.InfoLevel)
	t.Cleanup(func() {
		gin.DefaultWriter, gin.DefaultErrorWriter, log.Logger = prevOut, prevErr, prevLog
		zerolog.SetGlobalLevel(prevLevel)
	})
	return ginOut, zlOut
}

func newRouter(mode string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(ForMode(mode)...)
	r.GET("/ok", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.GET("/200", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	r.GET("/boom", func(*gin.Context) { panic("boom") })
	return r
}

func serve(r *gin.Engine, path string) int {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Code
}

func TestForMode_ReleaseLogsRequestsAsJSONOnly(t *testing.T) {
	ginOut, zlOut := captureLogs(t)
	r := newRouter(gin.ReleaseMode)

	if code := serve(r, "/ok?x=1"); code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204", code)
	}

	var line map[string]any
	if err := json.Unmarshal(zlOut.Bytes(), &line); err != nil {
		t.Fatalf("request log is not one JSON object: %v: %q", err, zlOut.String())
	}
	for key, want := range map[string]any{
		"level": "info", "method": "GET", "path": "/ok", "status": float64(204),
	} {
		if line[key] != want {
			t.Errorf("%s: got %v want %v", key, line[key], want)
		}
	}
	for _, key := range []string{"latency"} {
		if _, ok := line[key]; !ok {
			t.Errorf("missing %s", key)
		}
	}
	if ginOut.Len() != 0 {
		t.Errorf("gin text output in release: %q", ginOut.String())
	}
}

func TestForMode_ReleasePanicIsJSON500(t *testing.T) {
	ginOut, zlOut := captureLogs(t)
	r := newRouter(gin.ReleaseMode)

	if code := serve(r, "/boom"); code != http.StatusInternalServerError {
		t.Fatalf("status: got %d want 500", code)
	}
	lines := strings.Split(strings.TrimSpace(zlOut.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want panic + request log lines, got %q", zlOut.String())
	}
	for _, l := range lines {
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		if m["level"] != "error" {
			t.Errorf("level: got %v want error in %q", m["level"], l)
		}
	}
	if ginOut.Len() != 0 {
		t.Errorf("gin text output in release: %q", ginOut.String())
	}
}

func TestForMode_DebugUsesGinTextLogger(t *testing.T) {
	ginOut, zlOut := captureLogs(t)
	r := newRouter(gin.DebugMode)

	if code := serve(r, "/ok"); code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204", code)
	}
	if !strings.Contains(ginOut.String(), "[GIN]") || !strings.Contains(ginOut.String(), "/ok") {
		t.Errorf("want gin request line, got %q", ginOut.String())
	}
	if zlOut.Len() != 0 {
		t.Errorf("zerolog request line in debug: %q", zlOut.String())
	}
	if code := serve(r, "/boom"); code != http.StatusInternalServerError {
		t.Fatalf("panic status: got %d want 500", code)
	}
}

// Production runs zerolog at info level: a plain 200 must still be recorded.
func TestForMode_ProductionLogsSuccessfulRequest(t *testing.T) {
	_, zlOut := captureLogs(t) // global level info, as on production
	r := newRouter(gin.ReleaseMode)

	if code := serve(r, "/200"); code != http.StatusOK {
		t.Fatalf("status: got %d want 200", code)
	}
	var line map[string]any
	if err := json.Unmarshal(zlOut.Bytes(), &line); err != nil {
		t.Fatalf("200 not logged as JSON: %v: %q", err, zlOut.String())
	}
	if line["level"] != "info" || line["status"] != float64(200) || line["path"] != "/200" {
		t.Errorf("got %v, want info 200 /200", line)
	}
}

// Query values carry tokens and OAuth codes, and an address is not logged: only the parameter names are.
func TestRequestLogNeverRecordsQueryValuesOrTheClientAddress(t *testing.T) {
	for _, mode := range []string{gin.ReleaseMode, gin.DebugMode} {
		ginOut, zlOut := captureLogs(t)
		r := newRouter(mode)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/ok?token=SETUP-SECRET&code=OAUTH-CODE&state=abc", nil)
		req.RemoteAddr = "203.0.113.9:4444"
		req.Header.Set("X-Forwarded-For", "198.51.100.7")
		r.ServeHTTP(w, req)
		logged := zlOut.String() + ginOut.String()
		for _, secret := range []string{"SETUP-SECRET", "OAUTH-CODE", "203.0.113.9", "198.51.100.7"} {
			if strings.Contains(logged, secret) {
				t.Errorf("%s: the log contains %q: %s", mode, secret, logged)
			}
		}
		if mode == gin.ReleaseMode && !strings.Contains(zlOut.String(), `"query_keys":["code","state","token"]`) {
			t.Errorf("the parameter names are kept: %s", zlOut.String())
		}
	}
}

// logLines decodes the JSON lines zerolog wrote.
func logLines(t *testing.T, out *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("not JSON: %q", l)
		}
		lines = append(lines, m)
	}
	return lines
}

// Every line names the route template (empty for a path no route matches) and whether the caller was anonymous.
func TestRequestLogRecordsRouteAndAnonymous(t *testing.T) {
	_, zlOut := captureLogs(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(RequestLogger())
	r.GET("/items/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/me/:id", func(c *gin.Context) {
		claims := rbac.Claims{UserID: uuid.Must(uuid.NewV7())}
		c.Request = c.Request.WithContext(
			rbac.ContextWithCurrentUserSession(c.Request.Context(), claims),
		)
		c.Status(http.StatusOK)
	})
	for _, path := range []string{"/items/5", "/me/5", "/nowhere"} {
		serve(r, path)
	}
	lines := logLines(t, zlOut)
	if len(lines) != 3 {
		t.Fatalf("want 3 lines, got %q", zlOut.String())
	}
	for i, want := range []struct {
		route string
		anon  bool
	}{{"/items/:id", true}, {"/me/:id", false}, {"", true}} {
		if lines[i]["route"] != want.route || lines[i]["anon"] != want.anon {
			t.Errorf("line %d: route %v anon %v, want %q %v",
				i, lines[i]["route"], lines[i]["anon"], want.route, want.anon)
		}
	}
}

// 401, 429 and the overload 503 are sampled per status: the first line of a second is written at warn, the
// rest are counted and the count rides on the next written line of that status. Other statuses are not.
func TestRequestLogSamplesRefusals(t *testing.T) {
	_, zlOut := captureLogs(t)
	gin.SetMode(gin.TestMode)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	r := gin.New()
	r.Use(requestLogger(func() time.Time { return now }))
	r.GET("/401", func(c *gin.Context) { c.Status(http.StatusUnauthorized) })
	r.GET("/429", func(c *gin.Context) { c.Status(http.StatusTooManyRequests) })
	r.GET("/503", func(c *gin.Context) {
		c.Set(response.OverloadCtxKey, true)
		c.Status(http.StatusServiceUnavailable)
	})
	r.GET("/503-other", func(c *gin.Context) { c.Status(http.StatusServiceUnavailable) })
	r.GET("/403", func(c *gin.Context) { c.Status(http.StatusForbidden) })
	for range 100 {
		serve(r, "/401")
	}
	for range 3 {
		serve(r, "/429")
		serve(r, "/503")
		serve(r, "/503-other")
		serve(r, "/403")
	}
	counts := map[float64]int{}
	for _, line := range logLines(t, zlOut) {
		counts[line["status"].(float64)]++
		if _, ok := line["suppressed"]; ok {
			t.Errorf("no line of the first second carries suppressed: %v", line)
		}
	}
	// One each of 401, 429 and the overload 503; every 403 and every other 503 (3 each).
	if counts[401] != 1 || counts[429] != 1 || counts[503] != 1+3 || counts[403] != 3 {
		t.Fatalf("lines per status in the first second: %v", counts)
	}

	zlOut.Reset()
	now = now.Add(time.Second)
	serve(r, "/401")
	serve(r, "/503")
	lines := logLines(t, zlOut)
	if len(lines) != 2 {
		t.Fatalf("want one 401 and one 503 line in the next second, got %q", zlOut.String())
	}
	for i, want := range []struct{ status, suppressed float64 }{{401, 99}, {503, 2}} {
		got := lines[i]
		if got["status"] != want.status || got["suppressed"] != want.suppressed ||
			got["level"] != "warn" {
			t.Fatalf("next second's line %d: %v, want %+v", i, got, want)
		}
	}
}
