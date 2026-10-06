package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/pkg/err"
)

func TestWithErrorHandler_SendsRetryAfter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(WithErrorHandler)
	router.POST("/submit", func(ctx *gin.Context) {
		AbortWithError(ctx, err.ErrConflict.WithHTTPCode(http.StatusTooManyRequests).WithDetail(err.DetailRetryAfterSeconds, int64(18)).Err())
	})
	router.POST("/plain", func(ctx *gin.Context) {
		AbortWithError(ctx, err.ErrConflict.Err())
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/submit", nil))
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") != "18" {
		t.Fatalf("status=%d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/plain", nil))
	if rec.Code != http.StatusConflict || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("status=%d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
}

// Only the context an error marked public reaches the body: an agent name or any other WithContext value stays on the
// server, and an error with no public context has no Context at all.
func TestWithErrorHandler_SendsOnlyPublicContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(WithErrorHandler)
	router.GET("/mixed", func(ctx *gin.Context) {
		AbortWithError(ctx, err.ErrConflict.
			WithContext("agent", "eu-cluster-1").WithContext("ip", "10.0.0.7").
			WithPublicContext("nearest_from", "2026-10-05T09:00:00Z").WithPublicContext("window_minutes", 120).Err())
	})
	router.GET("/internal", func(ctx *gin.Context) {
		AbortWithError(ctx, err.ErrConflict.WithContext("agent", "eu-cluster-1").Err())
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mixed", nil))
	var body struct {
		Status struct{ Context map[string]any }
	}
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	if got := body.Status.Context; len(got) != 2 || got["nearest_from"] != "2026-10-05T09:00:00Z" || got["window_minutes"] != float64(120) {
		t.Fatalf("context = %v", got)
	}
	for _, secret := range []string{"eu-cluster-1", "10.0.0.7", "agent"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("a non-public context value reached the body: %s", rec.Body.String())
		}
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/internal", nil))
	if strings.Contains(rec.Body.String(), "Context") || strings.Contains(rec.Body.String(), "eu-cluster-1") {
		t.Fatalf("no public context, no Context: %s", rec.Body.String())
	}
}
