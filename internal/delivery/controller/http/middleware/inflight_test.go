package middleware

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

// inflightRig is a router behind the in-flight limiter whose handlers block until the test releases them, so
// a test can hold slots for as long as it needs.
type inflightRig struct {
	t       *testing.T
	r       *gin.Engine
	l       *InflightLimiter
	entered chan struct{}
	release chan struct{}
}

func newInflightRig(t *testing.T, cfg config.InflightConfig) *inflightRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	g := &inflightRig{
		t:       t,
		l:       NewInflightLimiter(cfg),
		entered: make(chan struct{}, 64),
		release: make(chan struct{}),
	}
	g.r = gin.New()
	g.r.Use(
		gin.CustomRecoveryWithWriter(
			io.Discard,
			func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) },
		),
		g.l.Handle,
	)
	block := func(c *gin.Context) {
		g.entered <- struct{}{}
		<-g.release
		c.Status(http.StatusOK)
	}
	g.r.GET("/api/x", block)
	g.r.GET("/api/health", block)
	g.r.GET("/api/events/:id/results/live", block)
	g.r.GET("/api/exercises/files/:fileID", block)
	g.r.GET("/api/panic", func(*gin.Context) { panic("handler failed") })
	g.r.NoRoute(block)
	t.Cleanup(func() { close(g.release) })
	return g
}

// start sends a request in the background; its recorder arrives on the returned channel once it is answered.
func (g *inflightRig) start(path string) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		g.r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		done <- w
	}()
	return done
}

// hold sends a request and waits until its handler runs, i.e. until it owns a slot.
func (g *inflightRig) hold(path string) {
	g.t.Helper()
	done := g.start(path)
	select {
	case <-g.entered:
	case w := <-done:
		g.t.Fatalf("%s should have been admitted and held, got %d", path, w.Code)
	case <-time.After(2 * time.Second):
		g.t.Fatalf("%s never reached its handler", path)
	}
}

// answered waits for a request that is expected to be answered without reaching a blocking handler.
func (g *inflightRig) answered(path string) *httptest.ResponseRecorder {
	g.t.Helper()
	select {
	case w := <-g.start(path):
		return w
	case <-g.entered:
		g.t.Fatalf("%s was admitted, it should have been refused", path)
	case <-time.After(2 * time.Second):
		g.t.Fatalf("%s got no answer", path)
	}
	return nil
}

func assertOverloaded(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Fatalf("want 503 with Retry-After 1, got %d / %q", w.Code, w.Header().Get("Retry-After"))
	}
	want := authModel.ErrServiceOverloaded.WithDetail("x", 1).Err().StatusCode().FullCode()
	var body struct {
		Status struct {
			Code int `json:"Code"`
		} `json:"Status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Status.Code != want {
		t.Fatalf("body %q: want code %d", w.Body.String(), want)
	}
}

// With every general slot held, the next request waits Wait for one and then gets the overload 503; a slot
// freed afterwards admits again (the refusal took nothing).
func TestInflightRefusesWhenFull(t *testing.T) {
	const wait = 30 * time.Millisecond
	g := newInflightRig(t, config.InflightConfig{Max: 2, Wait: wait})
	g.hold("/api/x")
	g.hold("/api/x")
	began := time.Now()
	assertOverloaded(t, g.answered("/api/x"))
	if took := time.Since(began); took < wait {
		t.Fatalf("refused after %v, before the %v wait was over", took, wait)
	}
	g.release <- struct{}{}
	g.hold("/api/x")
	assertOverloaded(t, g.answered("/api/x"))
}

func TestInflightAdmitsWithinWait(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, Wait: 50 * time.Millisecond})
	g.hold("/api/x")
	done := g.start("/api/x")
	time.Sleep(10 * time.Millisecond)
	g.release <- struct{}{} // the first request finishes; the waiting one takes its slot
	select {
	case <-g.entered:
	case w := <-done:
		t.Fatalf("the waiting request should have been admitted, got %d", w.Code)
	case <-time.After(2 * time.Second):
		t.Fatal("the waiting request never reached its handler")
	}
}

// A panicking handler still gives its slot back.
func TestInflightReleasesOnPanic(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 2})
	for range 5 {
		if w := g.answered("/api/panic"); w.Code != http.StatusInternalServerError {
			t.Fatalf("panic: %d", w.Code)
		}
	}
	g.hold("/api/x")
	g.hold("/api/x")
	assertOverloaded(t, g.answered("/api/x"))
}

// Live streams never take a general slot and have their own cap, refused at once (no wait).
func TestInflightStreamsSeparate(t *testing.T) {
	const wait = time.Second
	g := newInflightRig(t, config.InflightConfig{Max: 1, Wait: wait, StreamsMax: 1})
	g.hold("/api/x")
	g.hold("/api/events/1/results/live")
	began := time.Now()
	assertOverloaded(t, g.answered("/api/events/1/results/live"))
	if took := time.Since(began); took >= wait/2 {
		t.Fatalf("a stream over its cap must be refused at once, took %v", took)
	}
}

// Transfers never take a general slot and have their own cap.
func TestInflightTransfersSeparate(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, TransfersMax: 1})
	g.hold("/api/x")
	g.hold("/api/exercises/files/f1")
	assertOverloaded(t, g.answered("/api/exercises/files/f2"))
}

func TestInflightHealthNeverLimited(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, StreamsMax: 1, TransfersMax: 1})
	g.hold("/api/x")
	g.hold("/api/events/1/results/live")
	g.hold("/api/exercises/files/f1")
	for range 10 {
		g.hold("/api/health")
	}
}

// A path no route matches is a general request like any other.
func TestInflightUnmatchedPathIsGeneral(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, StreamsMax: 5, TransfersMax: 5})
	g.hold("/api/nope")
	assertOverloaded(t, g.answered("/api/x"))
}

func TestInflightZeroDisables(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{})
	for range 10 {
		g.hold("/api/x")
		g.hold("/api/events/1/results/live")
		g.hold("/api/exercises/files/f1")
	}
}

// Refusals are not logged one by one: one warn summary at most every 10 s, written by the first request after
// the interval.
func TestInflightLogsOneSummaryPerInterval(t *testing.T) {
	_, zlOut := captureLogs(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	g := newInflightRig(t, config.InflightConfig{Max: 1})
	g.l.now = func() time.Time { return now }
	g.l.lastReport.Store(now.UnixNano())
	g.hold("/api/x")
	for range 3 {
		assertOverloaded(t, g.answered("/api/x"))
	}
	if zlOut.Len() != 0 {
		t.Fatalf("logged before the interval: %q", zlOut.String())
	}
	now = now.Add(10 * time.Second)
	assertOverloaded(t, g.answered("/api/x"))
	assertOverloaded(t, g.answered("/api/x"))
	lines := strings.Split(strings.TrimSpace(zlOut.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one summary line, got %q", zlOut.String())
	}
	var line struct {
		Level           string `json:"level"`
		Rejected        int64  `json:"rejected"`
		RejectedGeneral int64  `json:"rejected_general"`
		PeakInflight    int64  `json:"peak_inflight"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatal(err)
	}
	if line.Level != "warn" || line.Rejected != 3 || line.RejectedGeneral != 3 ||
		line.PeakInflight != 1 {
		t.Fatalf("summary: %s", lines[0])
	}
}
