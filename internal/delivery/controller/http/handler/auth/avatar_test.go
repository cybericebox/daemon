package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

// The global ContentMiddleware presets Content-Type: application/json on every
// response. With X-Content-Type-Options: nosniff a browser then refuses to
// render the avatar, so the avatar route must replace it with the stored type.
func TestGetAvatar_ServesStoredContentTypeOverJSONDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ContentMiddleware)
	uc := &fakeUC{avatarBody: "\x89PNG", avatarType: "image/png"}
	h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/avatar/01a0d34b-007e-7fd4-b69b-4553cfe20376", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if got := w.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("Content-Type = %q, want image/png", got)
	}
	if got := w.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
