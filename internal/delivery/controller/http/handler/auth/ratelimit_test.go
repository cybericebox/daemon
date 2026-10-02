package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/limits"
)

// No public route is limited per client address: a whole computer lab shares one
// router at the start of an event. Floods are bounded per account and per recipient.
func TestPublicAuthRoutesAreNotLimitedPerClientAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	routes := []struct{ path, body string }{
		{"/api/auth/sign-in", `{"Email":"a@b.test","Password":"x"}`},
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
			for i := 0; i < 500; i++ {
				req := httptest.NewRequest(http.MethodPost, rt.path, strings.NewReader(rt.body))
				req.RemoteAddr = "203.0.113.9:4000"
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code == http.StatusTooManyRequests {
					t.Fatalf("request %d from one address was limited", i)
				}
			}
		})
	}
}

// The signed-in password check is limited per user, not per address.
func TestPasswordChangeIsLimitedPerUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limit := limits.Get().AccountActions
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	current := ids[0]
	r := gin.New()
	r.Use(func(c *gin.Context) { injectIdentity(current, uuid.Must(uuid.NewV7()))(c) })
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))
	post := func() int {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/password/change", strings.NewReader(`{"CurrentPassword":"a","NewPassword":"b"}`))
		req.RemoteAddr = "203.0.113.9:4000"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < limit; i++ {
		if post() == http.StatusTooManyRequests {
			t.Fatalf("request %d was limited", i)
		}
	}
	if post() != http.StatusTooManyRequests {
		t.Fatal("the request over the limit must be refused")
	}
	current = ids[1]
	if post() == http.StatusTooManyRequests {
		t.Fatal("another user from the same address must not be limited")
	}
}

// L7: the end of the account and giving away its Google login carry the owner's password in the body.
func TestDeleteAccountAndUnlinkGoogleReadTheCurrentPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct{ path, body, want string }{
		{"/api/auth/account", `{"CurrentPassword":"Secret!1"}`, "Secret!1"},
		{"/api/auth/account", ``, ""}, // an account without a password sends none
		{"/api/auth/google/link", `{"CurrentPassword":"Secret!1"}`, "Secret!1"},
	} {
		uc := &fakeUC{}
		r := gin.New()
		r.Use(injectIdentity(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())))
		h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
		h.Init(r.Group("api"), r.Group("api"))
		req := httptest.NewRequest(http.MethodDelete, tc.path, strings.NewReader(tc.body))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("%s %q: status %d: %s", tc.path, tc.body, w.Code, w.Body.String())
		}
		if uc.reauthPassword != tc.want {
			t.Fatalf("%s %q: password %q, want %q", tc.path, tc.body, uc.reauthPassword, tc.want)
		}
	}
}
