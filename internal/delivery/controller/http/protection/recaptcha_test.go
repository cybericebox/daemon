package protection_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

func newRecaptchaProt(cfg config.RecaptchaConfig) *protection.Protection {
	return protection.New(
		protection.Dependencies{
			UseCase: &fakeUseCase{},
			Config:  config.AuthConfig{Hosts: config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}, Recaptcha: cfg},
		},
	)
}

func TestRequireCaptcha_AlwaysEnforces(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newRecaptchaProt(
		config.RecaptchaConfig{},
	) // empty config — bypass removed, always enforces
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.POST("/x", p.RequireCaptcha("signIn"), func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"RecaptchaToken":""}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("always enforces: expected non-200 (missing token rejection), got 200")
	}
}

func TestRequireCaptcha_MissingToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newRecaptchaProt(config.RecaptchaConfig{SecretKey: "s"}) // configured → enforce
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.POST("/x", p.RequireCaptcha("signIn"), func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing token: want 400, got %d", w.Code)
	}
}
