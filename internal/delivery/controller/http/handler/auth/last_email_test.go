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
)

func lastEmailEngine(uc *fakeUC) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig).Init(r.Group("api"), r.Group("api"))
	return r
}

func cookieOf(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// A successful password sign-in remembers the address in a host-only, HttpOnly, Secure, Lax cookie for a year.
func TestSignIn_RemembersTheLastEmail(t *testing.T) {
	uc := &fakeUC{cookie: "session"}
	w := httptest.NewRecorder()
	lastEmailEngine(uc).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(`{"Email":"me@example.org","Password":"x"}`)))
	c := cookieOf(w, "cib_last_email")
	if c == nil || mustUnescape(c.Value) != "me@example.org" || c.MaxAge != 365*24*3600 || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Domain != "" || c.Path != "/" {
		t.Fatalf("cookie %+v", c)
	}
}

// When the address cannot be read the sign-in still succeeds, without the hint.
func TestSignIn_WithoutTheAddressStillSignsIn(t *testing.T) {
	uc := &fakeUC{cookie: "session", sessionEmailErr: errors.New("down")}
	w := httptest.NewRecorder()
	lastEmailEngine(uc).ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(`{"Email":"me@example.org","Password":"x"}`)))
	if w.Code != http.StatusOK || cookieOf(w, "cib_last_email") != nil {
		t.Fatalf("%d cookie %+v", w.Code, cookieOf(w, "cib_last_email"))
	}
}

func TestLastEmail_ReadAndForget(t *testing.T) {
	r := lastEmailEngine(&fakeUC{})
	req := httptest.NewRequest(http.MethodGet, "/api/auth/last-email", nil)
	req.AddCookie(&http.Cookie{Name: "cib_last_email", Value: url.QueryEscape("me@example.org")})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Email":"me@example.org"`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/last-email", nil))
	if !strings.Contains(w.Body.String(), `"Email":""`) {
		t.Fatalf("no cookie: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/auth/last-email", nil))
	if c := cookieOf(w, "cib_last_email"); w.Code != http.StatusOK || c == nil || c.MaxAge >= 0 {
		t.Fatalf("«not you» clears the cookie: %d %+v", w.Code, c)
	}
}

// Google's chooser is hinted with the remembered address, unless the page sent its own.
func TestGoogleStart_HintsTheRememberedAddress(t *testing.T) {
	uc := &fakeUC{}
	r := lastEmailEngine(uc)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/google", nil)
	req.AddCookie(&http.Cookie{Name: "cib_last_email", Value: url.QueryEscape("me@example.org")})
	r.ServeHTTP(httptest.NewRecorder(), req)
	if uc.loginHint != "me@example.org" {
		t.Fatalf("hint %q", uc.loginHint)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/auth/google?login_hint=other%40example.org", nil)
	req.AddCookie(&http.Cookie{Name: "cib_last_email", Value: "me%40example.org"})
	r.ServeHTTP(httptest.NewRecorder(), req)
	if uc.loginHint != "other@example.org" {
		t.Fatalf("an explicit hint wins: %q", uc.loginHint)
	}
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/auth/google", nil))
	if uc.loginHint != "" {
		t.Fatalf("no cookie, no hint: %q", uc.loginHint)
	}
}

func mustUnescape(v string) string {
	out, _ := url.QueryUnescape(v)
	return out
}
