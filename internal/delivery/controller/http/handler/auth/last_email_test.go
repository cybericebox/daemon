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
	lastEmailEngine(uc).ServeHTTP(w, withConsent(httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(`{"Email":"me@example.org","Password":"x"}`)), "analytics:denied,functional:granted"))
	c := cookieOf(w, "cib_last_email")
	if c == nil || mustUnescape(c.Value) != "me@example.org" || c.MaxAge != 365*24*3600 || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Domain != "" || c.Path != "/" {
		t.Fatalf("cookie %+v", c)
	}
}

// When the address cannot be read the sign-in still succeeds, without the hint.
func TestSignIn_WithoutTheAddressStillSignsIn(t *testing.T) {
	uc := &fakeUC{cookie: "session", sessionEmailErr: errors.New("down")}
	w := httptest.NewRecorder()
	lastEmailEngine(uc).ServeHTTP(w, withConsent(httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(`{"Email":"me@example.org","Password":"x"}`)), "functional:granted"))
	if w.Code != http.StatusOK || cookieOf(w, "cib_last_email") != nil {
		t.Fatalf("%d cookie %+v", w.Code, cookieOf(w, "cib_last_email"))
	}
}

func TestLastEmail_ReadAndForget(t *testing.T) {
	r := lastEmailEngine(&fakeUC{})
	req := withConsent(httptest.NewRequest(http.MethodGet, "/api/auth/last-email", nil), "analytics:granted,functional:granted")
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
	req := withConsent(httptest.NewRequest(http.MethodGet, "/api/auth/google", nil), "functional:granted")
	req.AddCookie(&http.Cookie{Name: "cib_last_email", Value: url.QueryEscape("me@example.org")})
	r.ServeHTTP(httptest.NewRecorder(), req)
	if uc.loginHint != "me@example.org" {
		t.Fatalf("hint %q", uc.loginHint)
	}
	req = withConsent(httptest.NewRequest(http.MethodGet, "/api/auth/google?login_hint=other%40example.org", nil), "functional:granted")
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

func withConsent(req *http.Request, value string) *http.Request {
	req.AddCookie(&http.Cookie{Name: "cib_consent", Value: value})
	return req
}

// Without functional consent (absent cookie, the old analytics-only format, or denied) the address is neither set,
// nor read, nor used as the Google hint; one remembered earlier is cleared.
func TestLastEmail_NeedsFunctionalConsent(t *testing.T) {
	for _, consent := range []string{"", "analytics:granted", "analytics:granted,functional:denied", "functional:granted2", "xfunctional:granted"} {
		t.Run("consent="+consent, func(t *testing.T) {
			uc := &fakeUC{cookie: "session"}
			r := lastEmailEngine(uc)
			signIn := httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(`{"Email":"me@example.org","Password":"x"}`))
			if consent != "" {
				withConsent(signIn, consent)
			}
			signIn.AddCookie(&http.Cookie{Name: "cib_last_email", Value: "old%40example.org"})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, signIn)
			c := cookieOf(w, "cib_last_email")
			if w.Code != http.StatusOK || c == nil || c.MaxAge >= 0 {
				t.Fatalf("sign-in: %d, the old address is cleared, nothing new is set: %+v", w.Code, c)
			}

			get := httptest.NewRequest(http.MethodGet, "/api/auth/last-email", nil)
			if consent != "" {
				withConsent(get, consent)
			}
			get.AddCookie(&http.Cookie{Name: "cib_last_email", Value: "old%40example.org"})
			w = httptest.NewRecorder()
			r.ServeHTTP(w, get)
			if !strings.Contains(w.Body.String(), `"Email":""`) {
				t.Fatalf("read without consent: %s", w.Body.String())
			}

			start := httptest.NewRequest(http.MethodGet, "/api/auth/google", nil)
			if consent != "" {
				withConsent(start, consent)
			}
			start.AddCookie(&http.Cookie{Name: "cib_last_email", Value: "old%40example.org"})
			r.ServeHTTP(httptest.NewRecorder(), start)
			if uc.loginHint != "" {
				t.Fatalf("the hint without consent: %q", uc.loginHint)
			}
		})
	}
}
