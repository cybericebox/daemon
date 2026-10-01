package token_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/golang-jwt/jwt/v5"

	"github.com/cybericebox/daemon/pkg/token"
)

// TestSetupToken_RoundTrip verifies the full generate→parse cycle for setup tokens,
// type isolation against other token kinds, and rejection of tampered/expired tokens.
func TestSetupToken_RoundTrip(t *testing.T) {
	c := mustClient(t)
	uid := uuid.Must(uuid.NewV7())

	t.Run("roundtrip", func(t *testing.T) {
		tok, err := c.GenerateSetupToken(uid)
		if err != nil {
			t.Fatalf("GenerateSetupToken: %v", err)
		}

		got, err := c.ParseSetupToken(tok)
		if err != nil {
			t.Fatalf("ParseSetupToken: %v", err)
		}
		if got != uid {
			t.Fatalf("user ID mismatch: got %v want %v", got, uid)
		}
	})

	t.Run("tampered_rejected", func(t *testing.T) {
		tok, err := c.GenerateSetupToken(uid)
		if err != nil {
			t.Fatalf("GenerateSetupToken: %v", err)
		}
		tampered := tok[:len(tok)-4] + "XXXX"
		_, err = c.ParseSetupToken(tampered)
		if err == nil {
			t.Fatal("expected error for tampered setup token")
		}
	})

	t.Run("expired_rejected", func(t *testing.T) {
		// Build a token with a past expiry using GenerateSetupTokenWithTTL (test-only helper).
		// Since we can't forge expiry via the public API, craft a raw JWT with the same
		// audience/issuer but a past exp using the exported signKey — we do this by
		// calling jwt directly with the package-internal key.
		// Instead, use the standard test approach: sign with a known key and past exp.
		//
		// We'll use token.NewWithExpiry (test helper) if exposed; otherwise test via
		// generating an expired token through a white-box approach.
		//
		// Given the package is external (_test), we rely on the fact that the library
		// validates exp: create a token with exp = -1 second using jwt directly.
		signKey := []byte("test-signing-key-that-is-long-enough")
		expiredClaims := jwt.MapClaims{
			"iss": "id",
			"sub": uid.String(),
			"aud": []string{"setup"},
			"exp": time.Now().Add(-time.Second).Unix(),
			"iat": time.Now().Add(-2 * time.Second).Unix(),
		}
		raw := jwt.NewWithClaims(jwt.SigningMethodHS256, expiredClaims)
		expiredTok, err := raw.SignedString(signKey)
		if err != nil {
			t.Fatalf("sign expired token: %v", err)
		}
		_, err = c.ParseSetupToken(expiredTok)
		if err == nil {
			t.Fatal("expected error for expired setup token")
		}
	})

	t.Run("setup_token_not_accepted_by_parse_session_cookie", func(t *testing.T) {
		tok, err := c.GenerateSetupToken(uid)
		if err != nil {
			t.Fatalf("GenerateSetupToken: %v", err)
		}
		// ParseSessionCookie must reject a setup token (different aud, different structure).
		_, err = c.ParseSessionCookie(tok)
		if err == nil {
			t.Fatal("ParseSessionCookie must not accept a setup token")
		}
	})

	t.Run("session_cookie_not_accepted_by_parse_setup_token", func(t *testing.T) {
		sessionID := uuid.Must(uuid.NewV7())
		cookieTok, err := c.GenerateSessionCookie(sessionID, time.Now().Add(time.Hour))
		if err != nil {
			t.Fatalf("GenerateSessionCookie: %v", err)
		}
		// ParseSetupToken must reject a session cookie (wrong aud).
		_, err = c.ParseSetupToken(cookieTok)
		if err == nil {
			t.Fatal("ParseSetupToken must not accept a session cookie token")
		}
	})
}

func TestSetupTokenUsesConfiguredTTL(t *testing.T) {
	c := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough", SetupTokenTTL: time.Minute})
	tok, err := c.GenerateSetupToken(uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := jwt.NewParser().ParseUnverified(tok, &jwt.RegisteredClaims{})
	if err != nil {
		t.Fatal(err)
	}
	exp, _ := parsed.Claims.GetExpirationTime()
	if d := time.Until(exp.Time); d > time.Minute || d < 50*time.Second {
		t.Fatalf("setup token lives %v, want about the configured minute", d)
	}
}
