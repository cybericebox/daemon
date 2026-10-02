package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
)

// M3: the unauthenticated recovery routes are flood-limited per client; the
// limit is far above any human use.
func TestPublicAuthRoutesAreRateLimitedPerClient(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := []struct{ path, body string }{
		{"/api/auth/password/reset-request", `{"Email":"a@b.test"}`},
		{"/api/auth/password/reset", `{"Code":"x","Password":"y"}`},
		{"/api/auth/sign-up", `{"Email":"a@b.test"}`},
		{"/api/auth/account/email/confirm", `{"Code":"x"}`},
	}
	for _, rt := range routes {
		t.Run(rt.path, func(t *testing.T) {
			r := gin.New()
			h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
			h.Init(r.Group("api"), r.Group("api"))

			var first, last int
			for i := 0; i < 61; i++ {
				req := httptest.NewRequest(http.MethodPost, rt.path, strings.NewReader(rt.body))
				req.RemoteAddr = "203.0.113.9:4000"
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if i == 0 {
					first = w.Code
				}
				last = w.Code
			}
			if first == http.StatusTooManyRequests {
				t.Fatal("the first request must not be limited")
			}
			if last != http.StatusTooManyRequests {
				t.Fatalf("the flood must hit 429, last status %d", last)
			}
		})
	}
}
