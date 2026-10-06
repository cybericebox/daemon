package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

// registerCallback drives the register-intent Google callback against uc.
func registerCallback(t *testing.T, uc *fakeUC, prot *fakeProt) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(uc, prot, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_intent", Value: "register"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("want 307, got %d body=%s", w.Code, w.Body.String())
	}
	return w
}

// Google already linked to a finished account: register signs in and lands
// on the use case's redirect instead of failing.
func TestGoogleCallback_Register_LinkedAccountSignsIn(t *testing.T) {
	prot := &fakeProt{}
	w := registerCallback(t, &fakeUC{googleRegResult: &authUseCase.GoogleRegistrationResult{
		SessionCookie: "sess-cookie",
		Redirect:      "https://id.example.test/profile/",
	}}, prot)

	if prot.capturedSessionCookie != "sess-cookie" {
		t.Fatalf("want Authenticate with session cookie, got %q", prot.capturedSessionCookie)
	}
	if loc := w.Header().Get("Location"); loc != "https://id.example.test/profile/" {
		t.Fatalf("want landing redirect, got %q", loc)
	}
}

// An active account owns the email but Google is not linked: send the user to
// sign-in with a specific error (no auto-link).
func TestGoogleCallback_Register_AccountExists_RedirectsToSignIn(t *testing.T) {
	prot := &fakeProt{}
	w := registerCallback(t, &fakeUC{googleRegErr: authModel.ErrAuthAccountExistsSignIn.Err()}, prot)

	if loc := w.Header().Get("Location"); loc != "https://id.example.test/sign-in/?google_error=already_registered" {
		t.Fatalf("want sign-in already_registered, got %q", loc)
	}
	if prot.capturedSessionCookie != "" {
		t.Fatal("must not authenticate")
	}
}

func TestGoogleCallback_Register_OtherError_RedirectsToSignUpFailed(t *testing.T) {
	w := registerCallback(t, &fakeUC{googleRegErr: errors.New("boom")}, &fakeProt{})

	if loc := w.Header().Get("Location"); loc != "https://id.example.test/sign-up/?google_error=failed" {
		t.Fatalf("want sign-up failed, got %q", loc)
	}
}

func TestGoogleCallback_Register_Blocked_RedirectsToSignInBlocked(t *testing.T) {
	w := registerCallback(t, &fakeUC{googleRegErr: authModel.ErrAuthAccountBlocked.Err()}, &fakeProt{})
	if loc := w.Header().Get("Location"); loc != "https://id.example.test/sign-in/?google_error=blocked" {
		t.Fatalf("want sign-in blocked, got %q", loc)
	}
}

func TestGoogleCallback_SignIn_Blocked_RedirectsToSignInBlocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	prot := &fakeProt{}
	h := authHandler.NewAuthAPIHandler(&fakeUC{googleAuthErr: authModel.ErrAuthAccountBlocked.Err()}, prot, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if loc := w.Header().Get("Location"); loc != "https://id.example.test/sign-in/?google_error=blocked" {
		t.Fatalf("want sign-in blocked, got %q", loc)
	}
	if prot.capturedSessionCookie != "" {
		t.Fatal("blocked user must not be authenticated")
	}
}

// L4: every OAuth cookie is host-only (__Host-): Secure, Path=/, no Domain, so
// a sibling subdomain cannot toss its own state into the browser.
func TestGoogleRedirectsSetHostPrefixedCookies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for path, wantCookies := range map[string][]string{
		"/api/auth/google":                     {"__Host-cib_oauth_state"},
		"/api/auth/google/register":            {"__Host-cib_oauth_state", "__Host-cib_oauth_intent"},
		"/api/auth/google/setup?token=setup-x": {"__Host-cib_oauth_state", "__Host-cib_oauth_intent", "__Host-cib_oauth_setup_token"},
	} {
		r := gin.New()
		h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
		h.Init(r.Group("api"), r.Group("api"))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		got := map[string]*http.Cookie{}
		for _, c := range w.Result().Cookies() {
			got[c.Name] = c
		}
		for _, name := range wantCookies {
			c, ok := got[name]
			if !ok {
				t.Errorf("%s: cookie %s not set (got %v)", path, name, got)
				continue
			}
			if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
				t.Errorf("%s: %s is not a valid __Host- cookie: %+v", path, name, c)
			}
		}
		for name := range got {
			if !strings.HasPrefix(name, "__Host-") {
				t.Errorf("%s: cookie %s lacks the __Host- prefix", path, name)
			}
		}
	}
}
