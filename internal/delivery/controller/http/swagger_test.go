package http_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	// register the swagger spec so swaggo serves the correct docs
	_ "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/apidocs"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// buildSwaggerRouter mirrors the swagger-conditional wiring in NewController.
func buildSwaggerRouter(enableSwagger bool) *gin.Engine {
	r := gin.New()
	if enableSwagger {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}
	return r
}

// newRequest creates a test request with RequestURI set so that ginSwagger's
// regexp matcher (which uses ctx.Request.RequestURI) works under httptest.
func newRequest(method, target string) *http.Request {
	req, _ := http.NewRequest(method, target, nil)
	req.RequestURI = target
	return req
}

// TestSwaggerRouteEnabled asserts that GET /swagger/doc.json returns 200
// when EnableSwagger is true.
func TestSwaggerRouteEnabled(t *testing.T) {
	router := buildSwaggerRouter(true)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newRequest(http.MethodGet, "/swagger/doc.json"))

	if w.Code != http.StatusOK {
		t.Fatalf(
			"expected 200 for /swagger/doc.json with EnableSwagger=true, got %d; body: %s",
			w.Code, w.Body.String(),
		)
	}
}

// TestSwaggerIndexEnabled asserts that GET /swagger/index.html returns 200
// when EnableSwagger is true.
func TestSwaggerIndexEnabled(t *testing.T) {
	router := buildSwaggerRouter(true)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newRequest(http.MethodGet, "/swagger/index.html"))

	if w.Code != http.StatusOK {
		t.Fatalf(
			"expected 200 for /swagger/index.html with EnableSwagger=true, got %d; body: %s",
			w.Code, w.Body.String(),
		)
	}
}

// TestSwaggerRouteDisabled asserts that GET /swagger/index.html does NOT return
// 200 when EnableSwagger is false (route is not registered, gin returns 404).
func TestSwaggerRouteDisabled(t *testing.T) {
	router := buildSwaggerRouter(false)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, newRequest(http.MethodGet, "/swagger/index.html"))

	if w.Code == http.StatusOK {
		t.Fatalf("expected non-200 for /swagger/index.html with EnableSwagger=false, got 200")
	}
}
