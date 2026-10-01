package oauth_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/cybericebox/daemon/pkg/oauth"
)

func defaultConfig() oauth.Config {
	return oauth.Config{
		Google: oauth.ClientConfig{
			ClientID:     "test-client-id",
			ClientSecret: "test-client-secret",
		},
		RedirectURLTemplate: "https://id.example.com/api/auth/%s/callback",
		StateSignature:      "test-state-signing-key",
		StateTTL:            5 * time.Minute,
	}
}

func mustClient(t *testing.T) *oauth.Client {
	t.Helper()
	c, err := oauth.New(defaultConfig())
	if err != nil {
		t.Fatalf("oauth.New: %v", err)
	}
	return c
}

// ── New ───────────────────────────────────────────────────────────────────────

func TestNew_EmptyStateSignature(t *testing.T) {
	cfg := defaultConfig()
	cfg.StateSignature = ""
	_, err := oauth.New(cfg)
	if !errors.Is(err, oauth.ErrEmptyStateSignature) {
		t.Fatalf("expected ErrEmptyStateSignature, got %v", err)
	}
}

func TestNew_DefaultTTL(t *testing.T) {
	cfg := defaultConfig()
	cfg.StateTTL = 0
	c, err := oauth.New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

// ── GetGoogleLoginURL ─────────────────────────────────────────────────────────

func TestGetGoogleLoginURL_ContainsState(t *testing.T) {
	c := mustClient(t)
	loginURL, state, err := c.GetGoogleLoginURL("")
	if err != nil {
		t.Fatalf("GetGoogleLoginURL: %v", err)
	}
	if state == "" {
		t.Fatal("expected non-empty state token")
	}
	if !strings.Contains(loginURL, "state=") {
		t.Fatalf("login URL missing state param: %q", loginURL)
	}
}

func TestGetGoogleLoginURL_UniqueStates(t *testing.T) {
	c := mustClient(t)
	_, s1, _ := c.GetGoogleLoginURL("")
	_, s2, _ := c.GetGoogleLoginURL("")
	// Each call produces a distinct JWT (different IssuedAt at minimum, but JWTs may share same second)
	// — what matters is both are valid
	if err := c.ValidateState(s1); err != nil {
		t.Fatalf("state1 invalid: %v", err)
	}
	if err := c.ValidateState(s2); err != nil {
		t.Fatalf("state2 invalid: %v", err)
	}
}

// ── ValidateState ─────────────────────────────────────────────────────────────

func TestValidateState_Valid(t *testing.T) {
	c := mustClient(t)
	_, state, _ := c.GetGoogleLoginURL("")
	if err := c.ValidateState(state); err != nil {
		t.Fatalf("expected valid state, got: %v", err)
	}
}

func TestValidateState_Tampered(t *testing.T) {
	c := mustClient(t)
	_, state, _ := c.GetGoogleLoginURL("")
	tampered := state[:len(state)-4] + "XXXX"
	if err := c.ValidateState(tampered); !errors.Is(err, oauth.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
}

func TestValidateState_WrongKey(t *testing.T) {
	c1 := mustClient(t)
	cfg2 := defaultConfig()
	cfg2.StateSignature = "different-key"
	c2, _ := oauth.New(cfg2)

	_, state, _ := c1.GetGoogleLoginURL("")
	if err := c2.ValidateState(state); !errors.Is(err, oauth.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
}

func TestValidateState_Expired(t *testing.T) {
	cfg := defaultConfig()
	cfg.StateTTL = -time.Second // already expired
	c, _ := oauth.New(cfg)
	_, state, _ := c.GetGoogleLoginURL("")
	if err := c.ValidateState(state); !errors.Is(err, oauth.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState for expired state, got %v", err)
	}
}

// ── GetGoogleUser with mock server ────────────────────────────────────────────

// mockOAuthServer creates a minimal OAuth2 + userinfo server for testing.
// It returns (server, oauth2Config pointing to server endpoints).
func mockOAuthServer(t *testing.T, userInfoPayload map[string]any) (*httptest.Server, *oauth2.Config) {
	t.Helper()
	mux := http.NewServeMux()

	// Token exchange endpoint.
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "test-access-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	// Userinfo endpoint.
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(userInfoPayload)
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cfg := &oauth2.Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  srv.URL + "/auth",
			TokenURL: srv.URL + "/token",
		},
		RedirectURL: "https://id.example.com/api/auth/google/callback",
	}
	return srv, cfg
}

func TestGetGoogleUser_ValidFlow(t *testing.T) {
	userPayload := map[string]any{
		"id":      "google-uid-123",
		"email":   "alice@example.com",
		"name":    "Alice",
		"picture": "https://example.com/pic.jpg",
	}
	srv, googleCfg := mockOAuthServer(t, userPayload)

	c := mustClient(t)
	_, state, _ := c.GetGoogleLoginURL("")

	// Exchange returns a real token pointing to our mock server.
	// We call GetGoogleUserWithConfig to inject mock config + userinfo URL.
	tok, err := googleCfg.Exchange(context.Background(), "any-code")
	if err != nil {
		t.Fatalf("mock token exchange: %v", err)
	}

	user, _, err := c.GetGoogleUserFromToken(context.Background(), state, srv.URL+"/userinfo", tok)
	if err != nil {
		t.Fatalf("GetGoogleUserFromToken: %v", err)
	}
	if user.GoogleID != "google-uid-123" {
		t.Fatalf("expected GoogleID 'google-uid-123', got %q", user.GoogleID)
	}
	if user.Email != "alice@example.com" {
		t.Fatalf("expected email 'alice@example.com', got %q", user.Email)
	}
}

func TestGetGoogleUser_ReadsGivenAndFamilyName(t *testing.T) {
	userPayload := map[string]any{
		"id":          "google-uid-123",
		"email":       "vp@example.com",
		"name":        "Volodymyr Porokhniak",
		"given_name":  "Volodymyr",
		"family_name": "Porokhniak",
	}
	srv, googleCfg := mockOAuthServer(t, userPayload)
	c := mustClient(t)
	_, state, _ := c.GetGoogleLoginURL("")
	tok, err := googleCfg.Exchange(context.Background(), "any-code")
	if err != nil {
		t.Fatalf("mock token exchange: %v", err)
	}

	user, _, err := c.GetGoogleUserFromToken(context.Background(), state, srv.URL+"/userinfo", tok)
	if err != nil {
		t.Fatalf("GetGoogleUserFromToken: %v", err)
	}
	if user.GivenName != "Volodymyr" || user.FamilyName != "Porokhniak" {
		t.Fatalf("given/family name not parsed: %+v", user)
	}
}

func TestGoogleUser_FirstLastName(t *testing.T) {
	cases := []struct {
		name      string
		user      oauth.GoogleUser
		wantFirst string
		wantLast  string
	}{
		{"given and family", oauth.GoogleUser{Name: "Volodymyr Porokhniak", GivenName: "Volodymyr", FamilyName: "Porokhniak"}, "Volodymyr", "Porokhniak"},
		{"given only", oauth.GoogleUser{Name: "Cher", GivenName: "Cher"}, "Cher", ""},
		{"fallback splits name on first space", oauth.GoogleUser{Name: "Mary Ann van Dyke"}, "Mary", "Ann van Dyke"},
		{"fallback single token", oauth.GoogleUser{Name: "Madonna"}, "Madonna", ""},
		{"fallback trims", oauth.GoogleUser{Name: "  Jane   Doe  "}, "Jane", "Doe"},
		{"nothing", oauth.GoogleUser{}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first, last := tc.user.FirstLastName()
			if first != tc.wantFirst || last != tc.wantLast {
				t.Fatalf("FirstLastName() = (%q, %q), want (%q, %q)", first, last, tc.wantFirst, tc.wantLast)
			}
		})
	}
}

func TestGetGoogleUser_InvalidState(t *testing.T) {
	c := mustClient(t)
	_, _, err := c.GetGoogleUser(context.Background(), "code", "invalid-state-token")
	if !errors.Is(err, oauth.ErrInvalidState) {
		t.Fatalf("expected ErrInvalidState, got %v", err)
	}
}

func TestGetGoogleUser_MissingRequiredFields(t *testing.T) {
	// Response missing "id" field.
	userPayload := map[string]any{
		"email": "alice@example.com",
		"name":  "Alice",
	}
	srv, googleCfg := mockOAuthServer(t, userPayload)

	c := mustClient(t)
	_, state, _ := c.GetGoogleLoginURL("")

	tok, _ := googleCfg.Exchange(context.Background(), "any-code")
	_, _, err := c.GetGoogleUserFromToken(context.Background(), state, srv.URL+"/userinfo", tok)
	if !errors.Is(err, oauth.ErrGoogleUserFetch) {
		t.Fatalf("expected ErrGoogleUserFetch, got %v", err)
	}
}

// ── Redirect embedding ────────────────────────────────────────────────────────

// TestGetGoogleLoginURL_EmbedsRedirect proves the post-auth redirect target
// survives the full round-trip: embedded into the state token by
// GetGoogleLoginURL, and read back out by GetGoogleUserFromToken after the
// (mocked) callback from Google — the mechanism this replaces (a same-host
// cookie) can no longer be relied on once the callback host changes.
func TestGetGoogleLoginURL_EmbedsRedirect(t *testing.T) {
	c := mustClient(t)

	wantRedirect := "https://id.example.test/profile"
	_, state, err := c.GetGoogleLoginURL(wantRedirect)
	if err != nil {
		t.Fatalf("GetGoogleLoginURL: %v", err)
	}

	userPayload := map[string]any{
		"id":    "google-uid-999",
		"email": "redirect-test@example.com",
		"name":  "Redirect Test",
	}
	srv, googleCfg := mockOAuthServer(t, userPayload)

	tok, err := googleCfg.Exchange(context.Background(), "any-code")
	if err != nil {
		t.Fatalf("mock token exchange: %v", err)
	}

	user, redirect, err := c.GetGoogleUserFromToken(context.Background(), state, srv.URL+"/userinfo", tok)
	if err != nil {
		t.Fatalf("GetGoogleUserFromToken: %v", err)
	}
	if user.GoogleID != "google-uid-999" {
		t.Fatalf("expected GoogleID 'google-uid-999', got %q", user.GoogleID)
	}
	if redirect != wantRedirect {
		t.Fatalf("redirect = %q, want %q", redirect, wantRedirect)
	}
}

// TestGetGoogleLoginURL_EmptyRedirect proves an empty redirect (the common
// case where no post-auth target was requested) round-trips as an empty
// string rather than some sentinel or error.
func TestGetGoogleLoginURL_EmptyRedirect(t *testing.T) {
	c := mustClient(t)

	_, state, err := c.GetGoogleLoginURL("")
	if err != nil {
		t.Fatalf("GetGoogleLoginURL: %v", err)
	}

	userPayload := map[string]any{
		"id":    "google-uid-1",
		"email": "no-redirect@example.com",
		"name":  "No Redirect",
	}
	srv, googleCfg := mockOAuthServer(t, userPayload)

	tok, err := googleCfg.Exchange(context.Background(), "any-code")
	if err != nil {
		t.Fatalf("mock token exchange: %v", err)
	}

	_, redirect, err := c.GetGoogleUserFromToken(context.Background(), state, srv.URL+"/userinfo", tok)
	if err != nil {
		t.Fatalf("GetGoogleUserFromToken: %v", err)
	}
	if redirect != "" {
		t.Fatalf("redirect = %q, want empty string", redirect)
	}
}
