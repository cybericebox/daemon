package auth_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

const eventURL = "https://event.example.test/e/1?x=y"

func serve(t *testing.T, uc *fakeUC, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSignUp_PassesRedirectToUseCase(t *testing.T) {
	uc := &fakeUC{}
	body := `{"Email":"a@b.test","Redirect":"` + eventURL + `"}`
	w := serve(t, uc, httptest.NewRequest(http.MethodPost, "/api/auth/sign-up", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if uc.signUpRedirect != eventURL {
		t.Fatalf("want Redirect passed through, got %q", uc.signUpRedirect)
	}
}

func TestGoogleRegisterRedirect_CarriesTrustedReturnTo(t *testing.T) {
	uc := &fakeUC{trustedRedirect: true}
	w := serve(t, uc, httptest.NewRequest(http.MethodGet, "/api/auth/google/register?return_to="+url.QueryEscape(eventURL), nil))
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("want 307, got %d", w.Code)
	}
	if uc.loginRedirect != eventURL {
		t.Fatalf("want return_to embedded in state, got %q", uc.loginRedirect)
	}
}

func TestGoogleRegisterRedirect_DropsUntrustedReturnTo(t *testing.T) {
	uc := &fakeUC{trustedRedirect: false}
	serve(t, uc, httptest.NewRequest(http.MethodGet, "/api/auth/google/register?return_to="+url.QueryEscape("https://evil.test"), nil))
	if uc.loginRedirect != "" {
		t.Fatalf("untrusted return_to must be dropped, got %q", uc.loginRedirect)
	}
}

func TestGoogleSetupRedirect_CarriesTrustedReturnTo(t *testing.T) {
	uc := &fakeUC{trustedRedirect: true}
	w := serve(t, uc, httptest.NewRequest(http.MethodGet, "/api/auth/google/setup?token=tok&return_to="+url.QueryEscape(eventURL), nil))
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("want 307, got %d", w.Code)
	}
	if uc.loginRedirect != eventURL {
		t.Fatalf("want return_to embedded in state, got %q", uc.loginRedirect)
	}
}

func callbackReq(intent string, extra ...*http.Cookie) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "cib_oauth_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "cib_oauth_intent", Value: intent})
	for _, c := range extra {
		req.AddCookie(c)
	}
	return req
}

func TestGoogleCallback_Register_SetupKeepsReturnTo(t *testing.T) {
	uc := &fakeUC{googleRegResult: &authUseCase.GoogleRegistrationResult{SetupToken: "setuptok", ReturnTo: eventURL}}
	w := serve(t, uc, callbackReq("register"))
	want := "https://id.example.test/setup?token=setuptok&return_to=" + url.QueryEscape(eventURL)
	if loc := w.Header().Get("Location"); loc != want {
		t.Fatalf("want %q, got %q", want, loc)
	}
}

func TestGoogleCallback_Setup_SuccessKeepsReturnTo(t *testing.T) {
	uc := &fakeUC{linkSetupReturnTo: eventURL}
	w := serve(t, uc, callbackReq("setup", &http.Cookie{Name: "cib_oauth_setup_token", Value: "tok"}))
	want := "https://id.example.test/setup?token=tok&return_to=" + url.QueryEscape(eventURL)
	if loc := w.Header().Get("Location"); loc != want {
		t.Fatalf("want %q, got %q", want, loc)
	}
}

// A failed Google link during setup returns to the setup page (the token is
// still valid) with error=link_failed instead of the generic sign-in failure.
func TestGoogleCallback_Setup_LinkFailedReturnsToSetup(t *testing.T) {
	uc := &fakeUC{linkSetupReturnTo: eventURL, linkSetupErr: errors.New("already linked elsewhere")}
	w := serve(t, uc, callbackReq("setup", &http.Cookie{Name: "cib_oauth_setup_token", Value: "tok"}))
	want := "https://id.example.test/setup?token=tok&error=link_failed&return_to=" + url.QueryEscape(eventURL)
	if loc := w.Header().Get("Location"); loc != want {
		t.Fatalf("want %q, got %q", want, loc)
	}
}

func TestGoogleCallback_Setup_MissingTokenCookieSignInFailed(t *testing.T) {
	w := serve(t, &fakeUC{}, callbackReq("setup"))
	if loc := w.Header().Get("Location"); loc != "https://id.example.test/sign-in?google_error=failed" {
		t.Fatalf("want sign-in failed, got %q", loc)
	}
}
