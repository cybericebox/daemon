package response

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/overload"
)

func overloadBody(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Status struct{ Code int }
	}
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	return body.Status.Code
}

func TestAbortWithServiceOverloaded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/busy", func(ctx *gin.Context) { AbortWithServiceOverloaded(ctx) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/busy", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if code := overloadBody(t, rec); code != 70431 {
		t.Fatalf("code=%d", code)
	}
}

func TestWithErrorHandlerMapsDBBusy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(WithErrorHandler)
	router.GET("/db", func(ctx *gin.Context) {
		AbortWithError(ctx, err.ErrInternal.WithError(fmt.Errorf("load: %w", overload.ErrDBBusy)).Err())
	})
	router.GET("/other", func(ctx *gin.Context) {
		AbortWithError(ctx, err.ErrInternal.WithError(fmt.Errorf("boom")).Err())
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/db", nil))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if code := overloadBody(t, rec); code != 70432 {
		t.Fatalf("code=%d", code)
	}

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/other", nil))
	if rec.Code != http.StatusInternalServerError || rec.Header().Get("Retry-After") != "" {
		t.Fatalf("status=%d Retry-After=%q", rec.Code, rec.Header().Get("Retry-After"))
	}
}
