package auth_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

// googleLinkFake records what the pending-link routes pass to the use case.
type googleLinkFake struct {
	*fakeUC
	ticket          string
	password, code  string
	pending         authUseCase.GoogleLinkPending
	err             error
	cancelled       bool
	confirmedCookie string
}

func (f *googleLinkFake) GoogleLinkPendingOf(_ context.Context, ticket string) (authUseCase.GoogleLinkPending, error) {
	f.ticket = ticket
	return f.pending, f.err
}
func (f *googleLinkFake) SendGoogleLinkCode(_ context.Context, ticket string) (authUseCase.GoogleLinkCodeSent, error) {
	f.ticket = ticket
	return authUseCase.GoogleLinkCodeSent{ResendAfter: time.Minute}, f.err
}
func (f *googleLinkFake) ConfirmGoogleLink(_ context.Context, ticket, password, code string, _ authModel.SessionMetadata) (string, string, error) {
	f.ticket, f.password, f.code = ticket, password, code
	if f.err != nil {
		return "", "", f.err
	}
	return f.confirmedCookie, "https://id.example.test/profile/", nil
}
func (f *googleLinkFake) CancelGoogleLink(_ context.Context, ticket string) error {
	f.ticket, f.cancelled = ticket, true
	return f.err
}

func linkEngine(t *testing.T, uc *googleLinkFake) (*gin.Engine, *fakeProt) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	prot := &fakeProt{}
	authHandler.NewAuthAPIHandler(uc, prot, testAuthConfig).Init(r.Group("api"), r.Group("api"))
	return r, prot
}

func doLink(r *gin.Engine, method, path, body, ticket string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ticket != "" {
		req.AddCookie(&http.Cookie{Name: "__Host-cib_google_link", Value: ticket})
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// The callback hands a non-Gmail identity to the confirmation page: the ticket in a cookie, never in the URL, and no
// session.
func TestGoogleCallback_ConfirmNeeded_SetsTheTicketCookieAndRedirects(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	uc := &fakeUC{googleAuthErr: &authUseCase.GoogleLinkConfirmError{Ticket: "ticket-value", ReturnTo: "https://id.example.test/profile/"}}
	prot := &fakeProt{}
	authHandler.NewAuthAPIHandler(uc, prot, testAuthConfig).Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://id.example.test/google-link/") || strings.Contains(loc, "ticket-value") {
		t.Fatalf("Location %q: the confirmation page, without the ticket", loc)
	}
	var ticketCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-cib_google_link" {
			ticketCookie = c
		}
	}
	if ticketCookie == nil || ticketCookie.Value != "ticket-value" || !ticketCookie.HttpOnly || !ticketCookie.Secure || ticketCookie.MaxAge != 600 {
		t.Fatalf("ticket cookie %+v", ticketCookie)
	}
	if prot.capturedSessionCookie != "" {
		t.Fatal("nobody is signed in before the confirmation")
	}
}

func TestGoogleLinkPending_ReadsTheTicketCookie(t *testing.T) {
	uc := &googleLinkFake{fakeUC: &fakeUC{}, pending: authUseCase.GoogleLinkPending{Email: "a***@example.org", HasPassword: true, GoogleEmail: "someone@example.org", GoogleName: "Some One"}}
	r, _ := linkEngine(t, uc)
	w := doLink(r, http.MethodGet, "/api/auth/google/pending", "", "tk")
	if w.Code != http.StatusOK || uc.ticket != "tk" || !strings.Contains(w.Body.String(), `"Email":"a***@example.org"`) || !strings.Contains(w.Body.String(), `"HasPassword":true`) ||
		!strings.Contains(w.Body.String(), `"GoogleEmail":"someone@example.org"`) || !strings.Contains(w.Body.String(), `"GoogleName":"Some One"`) {
		t.Fatalf("%d %s ticket=%q", w.Code, w.Body.String(), uc.ticket)
	}
	uc.err = authModel.ErrAuthGoogleLinkTicketInvalid.Err()
	if w = doLink(r, http.MethodGet, "/api/auth/google/pending", "", ""); w.Code != http.StatusNotFound {
		t.Fatalf("no ticket: %d", w.Code)
	}
}

func TestGoogleLinkSendCode(t *testing.T) {
	uc := &googleLinkFake{fakeUC: &fakeUC{}}
	r, _ := linkEngine(t, uc)
	w := doLink(r, http.MethodPost, "/api/auth/google/pending/code", "", "tk")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ResendAfterSeconds":60`) {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestGoogleLinkConfirm_SignsInAndClearsTheTicket(t *testing.T) {
	uc := &googleLinkFake{fakeUC: &fakeUC{}, confirmedCookie: "session-cookie"}
	r, prot := linkEngine(t, uc)
	w := doLink(r, http.MethodPost, "/api/auth/google/pending/confirm", `{"Password":"pw"}`, "tk")
	if w.Code != http.StatusOK || uc.ticket != "tk" || uc.password != "pw" || uc.code != "" || prot.capturedSessionCookie != "session-cookie" {
		t.Fatalf("%d %s uc=%+v session=%q", w.Code, w.Body.String(), uc, prot.capturedSessionCookie)
	}
	if !strings.Contains(w.Body.String(), `"RedirectURL":"https://id.example.test/profile/"`) {
		t.Fatalf("body %s", w.Body.String())
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-cib_google_link" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the used ticket cookie is cleared")
	}
}

func TestGoogleLinkConfirm_RefusalKeepsTheTicketAndSignsNobodyIn(t *testing.T) {
	uc := &googleLinkFake{fakeUC: &fakeUC{}, err: authModel.ErrAuthInvalidUserCredentials.Err()}
	r, prot := linkEngine(t, uc)
	w := doLink(r, http.MethodPost, "/api/auth/google/pending/confirm", `{"Password":"wrong"}`, "tk")
	if w.Code == http.StatusOK || prot.capturedSessionCookie != "" {
		t.Fatalf("%d session=%q", w.Code, prot.capturedSessionCookie)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-cib_google_link" {
			t.Fatalf("a failed attempt must not touch the ticket cookie: %+v", c)
		}
	}
	if w = doLink(r, http.MethodPost, "/api/auth/google/pending/confirm", `not json`, "tk"); w.Code != http.StatusBadRequest {
		t.Fatalf("bad body: %d", w.Code)
	}
}

func TestGoogleLinkCancel(t *testing.T) {
	uc := &googleLinkFake{fakeUC: &fakeUC{}}
	r, _ := linkEngine(t, uc)
	w := doLink(r, http.MethodDelete, "/api/auth/google/pending", "", "tk")
	if w.Code != http.StatusOK || !uc.cancelled || uc.ticket != "tk" {
		t.Fatalf("%d %+v", w.Code, uc)
	}
}
