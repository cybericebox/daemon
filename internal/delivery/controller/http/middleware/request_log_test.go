package middleware

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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
		"level": "info", "method": "GET", "path": "/ok", "query": "x=1", "status": float64(204),
	} {
		if line[key] != want {
			t.Errorf("%s: got %v want %v", key, line[key], want)
		}
	}
	for _, key := range []string{"latency", "ip"} {
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
