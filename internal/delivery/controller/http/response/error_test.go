package response

import (
	"net/http"
	"net/http/httptest"
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
