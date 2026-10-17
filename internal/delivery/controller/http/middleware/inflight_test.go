package middleware

import (
	"bytes"
	"context"
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

// testSessionKey stands in for the gate's cookie check: a session cookie "valid-<id>" opens as session <id>,
// anything else (a garbage cookie) does not.
func testSessionKey(c *gin.Context) (string, bool) {
	cookie, err := c.Cookie(authModel.SessionCookie)
	if err != nil {
		return "", false
	}
	return strings.CutPrefix(cookie, "valid-")
}

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
		l:       NewInflightLimiter(cfg, testSessionKey),
		entered: make(chan struct{}, 128),
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
	g.r.POST("/api/x", func(c *gin.Context) {
		_, _ = io.ReadAll(c.Request.Body) // like a real handler, it reads the body first
		block(c)
	})
	g.r.GET("/api/health", block)
	g.r.GET("/api/events/:id/results/live", block)
	g.r.GET("/api/exercises/files/:fileID", block)
	g.r.GET("/api/panic", func(*gin.Context) { panic("handler failed") })
	g.r.POST("/api/echo", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Data(http.StatusOK, "application/octet-stream", body)
	})
	g.r.NoRoute(block)
	t.Cleanup(func() { close(g.release) })
	return g
}

// request is one test request: a path, plus optional session cookie, body and request context.
type request struct {
	path    string
	cookie  string
	body    io.Reader
	context context.Context
}

func get(path string) request { return request{path: path} }

func (q request) build() *http.Request {
	method := http.MethodGet
	if q.body != nil {
		method = http.MethodPost
	}
	req := httptest.NewRequest(method, q.path, q.body)
	if q.cookie != "" {
		req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: q.cookie})
	}
	if q.context != nil {
		req = req.WithContext(q.context)
	}
	return req
}

// start sends a request in the background; its recorder arrives on the returned channel once it is answered.
func (g *inflightRig) start(q request) <-chan *httptest.ResponseRecorder {
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		g.r.ServeHTTP(w, q.build())
		done <- w
	}()
	return done
}

// hold sends a request and waits until its handler runs, i.e. until it owns a slot; the returned channel
// delivers its answer once the test releases it.
func (g *inflightRig) hold(q request) <-chan *httptest.ResponseRecorder {
	g.t.Helper()
	done := g.start(q)
	select {
	case <-g.entered:
	case w := <-done:
		g.t.Fatalf("%s should have been admitted and held, got %d", q.path, w.Code)
	case <-time.After(5 * time.Second):
		g.t.Fatalf("%s never reached its handler", q.path)
	}
	return done
}

// answered waits for a request that is expected to be answered without reaching a blocking handler.
func (g *inflightRig) answered(q request) *httptest.ResponseRecorder {
	g.t.Helper()
	select {
	case w := <-g.start(q):
		return w
	case <-g.entered:
		g.t.Fatalf("%s was admitted, it should have been refused", q.path)
	case <-time.After(5 * time.Second):
		g.t.Fatalf("%s got no answer", q.path)
	}
	return nil
}

// finish releases one held request and waits until it is answered, i.e. until its slot is free again.
func (g *inflightRig) finish(done <-chan *httptest.ResponseRecorder) {
	g.t.Helper()
	g.release <- struct{}{}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		g.t.Fatal("the released request was never answered")
	}
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
	first := g.hold(get("/api/x"))
	g.hold(get("/api/x"))
	began := time.Now()
	assertOverloaded(t, g.answered(get("/api/x")))
	if took := time.Since(began); took < wait {
		t.Fatalf("refused after %v, before the %v wait was over", took, wait)
	}
	g.finish(first)
	g.hold(get("/api/x"))
	assertOverloaded(t, g.answered(get("/api/x")))
}

func TestInflightAdmitsWithinWait(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, Wait: time.Second})
	g.hold(get("/api/x"))
	done := g.start(get("/api/x"))
	time.Sleep(10 * time.Millisecond)
	g.release <- struct{}{} // the first request finishes; the waiting one takes its slot
	select {
	case <-g.entered:
	case w := <-done:
		t.Fatalf("the waiting request should have been admitted, got %d", w.Code)
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting request never reached its handler")
	}
}

// A client that goes away while it waits is not a refusal: no answer is written and nothing is counted.
func TestInflightClientGoneWhileWaiting(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, Wait: time.Second})
	g.hold(get("/api/x"))
	ctx, cancel := context.WithCancel(context.Background())
	done := g.start(request{path: "/api/x", context: ctx})
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case w := <-done:
		if w.Body.Len() != 0 || w.Header().Get("Retry-After") != "" {
			t.Fatalf(
				"nothing should be written to a client that left: %d %q",
				w.Code,
				w.Body.String(),
			)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the request of a client that left never ended")
	}
	if n := g.l.rejected[inflightGeneral].Load(); n != 0 {
		t.Fatalf("a client that left counted as %d refusals", n)
	}
}

// A panicking handler still gives its slot back.
func TestInflightReleasesOnPanic(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 2})
	for range 5 {
		if w := g.answered(get("/api/panic")); w.Code != http.StatusInternalServerError {
			t.Fatalf("panic: %d", w.Code)
		}
	}
	g.hold(get("/api/x"))
	g.hold(get("/api/x"))
	assertOverloaded(t, g.answered(get("/api/x")))
}

// Live streams never take a general slot and have their own cap, refused at once (no wait).
func TestInflightStreamsSeparate(t *testing.T) {
	const wait = 2 * time.Second
	g := newInflightRig(t, config.InflightConfig{Max: 1, Wait: wait, StreamsMax: 1})
	g.hold(get("/api/x"))
	g.hold(get("/api/events/1/results/live"))
	began := time.Now()
	assertOverloaded(t, g.answered(get("/api/events/1/results/live")))
	if took := time.Since(began); took >= wait/2 {
		t.Fatalf("a stream over its cap must be refused at once, took %v", took)
	}
}

// Transfers never take a general slot and have their own cap.
func TestInflightTransfersSeparate(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, TransfersMax: 1})
	g.hold(get("/api/x"))
	g.hold(get("/api/exercises/files/f1"))
	assertOverloaded(t, g.answered(get("/api/exercises/files/f2")))
}

func TestInflightHealthNeverLimited(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, StreamsMax: 1, TransfersMax: 1})
	g.hold(get("/api/x"))
	g.hold(get("/api/events/1/results/live"))
	g.hold(get("/api/exercises/files/f1"))
	for range 10 {
		g.hold(get("/api/health"))
	}
}

// A path no route matches is a general request like any other.
func TestInflightUnmatchedPathIsGeneral(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, StreamsMax: 5, TransfersMax: 5})
	g.hold(get("/api/nope"))
	assertOverloaded(t, g.answered(get("/api/x")))
}

func TestInflightZeroDisables(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{})
	for range 10 {
		g.hold(get("/api/x"))
		g.hold(get("/api/events/1/results/live"))
		g.hold(get("/api/exercises/files/f1"))
	}
}

// Anonymous requests hold at most AnonShare percent of the general slots; a signed-in caller still gets in.
func TestInflightAnonShareCapped(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 64, AnonShare: 25, PerSession: 8})
	for range 16 {
		g.hold(get("/api/x"))
	}
	assertOverloaded(t, g.answered(get("/api/x")))
	g.hold(request{path: "/api/x", cookie: "valid-a"})
}

// One session holds at most PerSession requests at once; another session is not affected.
func TestInflightPerSessionCap(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 64, AnonShare: 25, PerSession: 8})
	for range 8 {
		g.hold(request{path: "/api/x", cookie: "valid-a"})
	}
	assertOverloaded(t, g.answered(request{path: "/api/x", cookie: "valid-a"}))
	g.hold(request{path: "/api/x", cookie: "valid-b"})
}

// A cookie that does not open is anonymous: it cannot be used to dodge the anonymous share.
func TestInflightGarbageCookieIsAnonymous(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 8, AnonShare: 25, PerSession: 8})
	g.hold(request{path: "/api/x", cookie: "garbage-1"})
	g.hold(get("/api/x"))
	assertOverloaded(t, g.answered(request{path: "/api/x", cookie: "garbage-2"}))
}

// The internal listener (in-cluster SSR) is one trusted caller fronting many visitors: neither the anonymous
// share nor the per-session cap applies, only the general cap.
func TestInflightInternalListenerNotAnonCapped(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 8, AnonShare: 25, PerSession: 1})
	internal := WithInternalListener(context.Background())
	for range 4 {
		g.hold(request{path: "/api/x", context: internal})
		g.hold(request{path: "/api/x", context: internal, cookie: "valid-a"})
	}
	assertOverloaded(t, g.answered(request{path: "/api/x", context: internal}))
}

// trickle is a body that sends nothing until the test lets it.
type trickle struct {
	sent chan struct{}
	r    io.Reader
}

func (b *trickle) Read(p []byte) (int, error) {
	<-b.sent
	return b.r.Read(p)
}

// A client that is slow to send its body does not hold a general slot while it sends.
func TestInflightSlowBodyDoesNotHoldSlot(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1, Wait: time.Second})
	slow := &trickle{sent: make(chan struct{}), r: strings.NewReader(`{"a":1}`)}
	slowDone := g.start(request{path: "/api/x", body: slow})
	time.Sleep(10 * time.Millisecond)
	fast := g.hold(get("/api/x"))
	close(slow.sent)
	g.finish(fast)
	g.finish(slowDone)
}

// A buffered body reaches the handler whole; a body over the buffer streams through after the slot is taken.
func TestInflightBodyReachesHandler(t *testing.T) {
	g := newInflightRig(t, config.InflightConfig{Max: 1})
	for _, size := range []int{0, 17, inflightBufferMax, inflightBufferMax + 4096} {
		want := bytes.Repeat([]byte("b"), size)
		w := g.answered(request{path: "/api/echo", body: bytes.NewReader(want)})
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), want) {
			t.Fatalf("size %d: %d, %d bytes back", size, w.Code, w.Body.Len())
		}
	}
}

// A body over the global cap still fails in the handler, as without the limiter.
func TestInflightBufferedBodyKeepsTheCap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(BodyLimit(16), NewInflightLimiter(config.InflightConfig{Max: 1}, nil).Handle)
	r.POST("/api/echo", func(c *gin.Context) {
		if _, err := io.ReadAll(c.Request.Body); err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/echo",
			strings.NewReader(strings.Repeat("a", 64)),
		),
	)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("a body over the cap: %d", w.Code)
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
	g.hold(get("/api/x"))
	for range 3 {
		assertOverloaded(t, g.answered(get("/api/x")))
	}
	if zlOut.Len() != 0 {
		t.Fatalf("logged before the interval: %q", zlOut.String())
	}
	now = now.Add(10 * time.Second)
	assertOverloaded(t, g.answered(get("/api/x")))
	assertOverloaded(t, g.answered(get("/api/x")))
	lines := strings.Split(strings.TrimSpace(zlOut.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("want one summary line, got %q", zlOut.String())
	}
	var line struct {
		Level           string `json:"level"`
		Rejected        int64  `json:"rejected"`
		RejectedGeneral int64  `json:"rejected_general"`
		PeakGeneral     int64  `json:"peak_general"`
		PeakStreams     int64  `json:"peak_streams"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &line); err != nil {
		t.Fatal(err)
	}
	if line.Level != "warn" || line.Rejected != 3 || line.RejectedGeneral != 3 ||
		line.PeakGeneral != 1 ||
		line.PeakStreams != 0 {
		t.Fatalf("summary: %s", lines[0])
	}
}
