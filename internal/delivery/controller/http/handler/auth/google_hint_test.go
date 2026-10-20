package auth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

// A setup link whose Google identity has another address returns to setup with its own error, so the page can
// say which address to pick.
func TestGoogleCallback_Setup_EmailMismatchReturnsItsOwnError(t *testing.T) {
	uc := &fakeUC{
		linkSetupReturnTo: eventURL,
		linkSetupErr:      authModel.ErrAuthGoogleEmailMismatch.Err(),
	}
	w := serve(
		t,
		uc,
		callbackReq("setup", &http.Cookie{Name: "__Host-cib_oauth_setup_token", Value: "tok"}),
	)
	want := "https://id.example.test/setup/?token=tok&error=email_mismatch&return_to=" + url.QueryEscape(
		eventURL,
	)
	if loc := w.Header().Get("Location"); loc != want {
		t.Fatalf("want %q, got %q", want, loc)
	}
}

// Sign-in and register with Google pass the address the page knows on as the chooser hint.
func TestGoogleStart_PassesLoginHint(t *testing.T) {
	for _, path := range []string{"/api/auth/google", "/api/auth/google/register"} {
		uc := &fakeUC{}
		req := httptest.NewRequest(
			http.MethodGet,
			path+"?login_hint="+url.QueryEscape("a@b.test"),
			nil,
		)
		if w := serve(t, uc, req); w.Code != http.StatusTemporaryRedirect {
			t.Fatalf("%s: want 307, got %d", path, w.Code)
		}
		if uc.loginHint != "a@b.test" {
			t.Fatalf("%s: login hint = %q", path, uc.loginHint)
		}
	}
}

// The setup start hints from the account behind the setup token (the use case looks it up).
func TestGoogleSetupStart_UsesTheSetupToken(t *testing.T) {
	uc := &fakeUC{}
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/auth/google/setup?token=tok&login_hint="+url.QueryEscape("other@b.test"),
		nil,
	)
	if w := serve(t, uc, req); w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("want 307, got %d", w.Code)
	}
	if uc.setupStartToken != "tok" || uc.loginHint != "" {
		t.Fatalf(
			"setup start: token=%q hint=%q (a query hint must not override the account)",
			uc.setupStartToken,
			uc.loginHint,
		)
	}
}

// Linking from the profile with a Google account of another address lands on the connections section with its
// own error, so the page can name the address to pick; other failures keep link_failed.
func TestGoogleCallback_Link_EmailMismatchReturnsItsOwnError(t *testing.T) {
	sid := &http.Cookie{Name: "__Host-cib_oauth_link_sid", Value: "sess"}
	uc := &fakeUC{linkErr: authModel.ErrAuthGoogleEmailMismatch.Err()}
	w := serve(t, uc, callbackReq("link", sid))
	mismatch := "https://id.example.test/profile/?tab=connections&error=email_mismatch"
	if loc := w.Header().Get("Location"); loc != mismatch {
		t.Fatalf("got %q", loc)
	}
	other := &fakeUC{linkErr: authModel.ErrAuthGoogleEmailNotVerified.Err()}
	w = serve(t, other, callbackReq("link", sid))
	failed := "https://id.example.test/profile/?error=link_failed"
	if loc := w.Header().Get("Location"); loc != failed {
		t.Fatalf("other failure: got %q", loc)
	}
}
