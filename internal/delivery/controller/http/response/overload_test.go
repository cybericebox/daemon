package response_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/overload"
)

func overloadBody(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	var body struct {
		Status struct {
			Code int `json:"Code"`
		} `json:"Status"`
	}
	if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	return body.Status.Code
}

func TestAbortWithServiceOverloaded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/busy", func(ctx *gin.Context) { response.AbortWithServiceOverloaded(ctx) })

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
	router.Use(response.WithErrorHandler)
	router.GET("/db", func(ctx *gin.Context) {
		wrapped := fmt.Errorf("load: %w", overload.ErrDBBusy)
		response.AbortWithError(ctx, err.ErrInternal.WithError(wrapped).Err())
	})
	router.GET("/other", func(ctx *gin.Context) {
		response.AbortWithError(ctx, err.ErrInternal.WithError(errors.New("boom")).Err())
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
