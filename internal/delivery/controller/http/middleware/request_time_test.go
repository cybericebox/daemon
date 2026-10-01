package middleware_test

import (
	"net/http"
	"net/http/httptest"
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
