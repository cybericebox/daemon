package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	controller "github.com/cybericebox/daemon/internal/delivery/controller/http"
)

// GET /api/health is the public liveness probe the frontends poll while the
// API is unreachable: 200 whenever the process can serve requests.
func TestHealth_ReturnsOK(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	controller.RegisterHealth(r)

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/health", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}
