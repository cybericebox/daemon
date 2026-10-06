package middleware_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

func TestCaptureRequestReceivedAt_PersistsArrivalAcrossHandlerWork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CaptureRequestReceivedAt())
	var received time.Time
	r.GET("/", func(c *gin.Context) {
		received = middleware.RequestReceivedAt(c.Request.Context())
		time.Sleep(time.Millisecond)
		if !middleware.RequestReceivedAt(c.Request.Context()).Equal(received) {
			t.Fatal("arrival time changed during request processing")
		}
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusOK || received.IsZero() {
		t.Fatalf("status=%d received=%v", w.Code, received)
	}
}

// slowBody delivers its payload only after a delay: a client that sent the
// headers at once and holds the answer back.
type slowBody struct {
	delay time.Duration
	data  string
	read  bool
}

func (b *slowBody) Read(p []byte) (int, error) {
	if b.read {
		return 0, io.EOF
	}
	time.Sleep(b.delay)
	b.read = true
	return copy(p, b.data), nil
}
func (b *slowBody) Close() error { return nil }

// M8 PoC: the submission time was stamped when the headers arrived, so an answer
// held back for seconds was still dated at the headers' arrival (first blood,
// deadline). It is counted from the end of the body.
func TestReceivedAfterBody_StampsWhenTheBodyIsComplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CaptureRequestReceivedAt())
	var received time.Time
	var body string
	r.POST("/submit", middleware.ReceivedAfterBody, func(c *gin.Context) {
		received = middleware.RequestReceivedAt(c.Request.Context())
		b, _ := io.ReadAll(c.Request.Body)
		body = string(b)
	})
	req := httptest.NewRequest(http.MethodPost, "/submit", nil)
	req.Body = &slowBody{delay: 150 * time.Millisecond, data: `{"Answer":"x"}`}
	headersAt := time.Now()
	r.ServeHTTP(httptest.NewRecorder(), req)

	if body != `{"Answer":"x"}` {
		t.Fatalf("the handler must still get the body, got %q", body)
	}
	if received.Sub(headersAt) < 140*time.Millisecond {
		t.Fatalf("received at +%v: the held-back body must not be back-dated to the headers", received.Sub(headersAt))
	}
}

func TestReceivedAfterBody_ReplaysAReadErrorToTheHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var readErr error
	r.POST("/submit", func(c *gin.Context) { c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4); c.Next() },
		middleware.ReceivedAfterBody, func(c *gin.Context) { _, readErr = io.ReadAll(c.Request.Body) })
	req := httptest.NewRequest(http.MethodPost, "/submit", strings.NewReader("way too long a body"))
	r.ServeHTTP(httptest.NewRecorder(), req)
	if readErr == nil {
		t.Fatal("the oversized body error must reach the handler's own bind")
	}
}
