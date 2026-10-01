package token_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/pkg/token"
)

// validConfig returns a well-formed Config for tests.
func validConfig() token.Config {
	return token.Config{
		TokenSignature: "test-signing-key-that-is-long-enough",
	}
}

func mustClient(t *testing.T) *token.Client {
	t.Helper()
	c, err := token.New(validConfig())
	if err != nil {
		t.Fatalf("token.New: %v", err)
	}
	return c
}

// ── New / MustNew ──────────────────────────────────────────────────────────

func TestNew_EmptySignKey(t *testing.T) {
	cfg := validConfig()
	cfg.TokenSignature = ""
	_, err := token.New(cfg)
	if err == nil {
		t.Fatal("expected error for empty sign key")
	}
}

func TestMustNew_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic for invalid config")
		}
	}()
	token.MustNew(token.Config{})
}

// ── Session cookie ─────────────────────────────────────────────────────────

func TestSessionCookie_RoundTrip(t *testing.T) {
	c := mustClient(t)
	sessionID := uuid.Must(uuid.NewV7())
	expiresAt := time.Now().Add(time.Hour)

	tok, err := c.GenerateSessionCookie(sessionID, expiresAt)
	if err != nil {
		t.Fatalf("GenerateSessionCookie: %v", err)
	}

	got, err := c.ParseSessionCookie(tok)
	if err != nil {
		t.Fatalf("ParseSessionCookie: %v", err)
	}
	if got != sessionID {
		t.Fatalf("session ID mismatch: got %v want %v", got, sessionID)
	}
}

func TestSessionCookie_Expired(t *testing.T) {
	c := mustClient(t)
	sessionID := uuid.Must(uuid.NewV7())
	expired := time.Now().Add(-time.Second)

	tok, _ := c.GenerateSessionCookie(sessionID, expired)
	_, err := c.ParseSessionCookie(tok)
	if err == nil {
		t.Fatal("expected error for expired cookie")
	}
}

func TestSessionCookie_WrongKey(t *testing.T) {
	c1 := mustClient(t)
	cfg2 := validConfig()
	cfg2.TokenSignature = "different-key"
	c2, _ := token.New(cfg2)

	sessionID := uuid.Must(uuid.NewV7())
	tok, _ := c1.GenerateSessionCookie(sessionID, time.Now().Add(time.Hour))

	_, err := c2.ParseSessionCookie(tok)
	if err == nil {
		t.Fatal("expected error for wrong signing key")
	}
}

func TestSessionCookie_Tampered(t *testing.T) {
	c := mustClient(t)
	sessionID := uuid.Must(uuid.NewV7())
	tok, _ := c.GenerateSessionCookie(sessionID, time.Now().Add(time.Hour))

	tampered := tok[:len(tok)-4] + "XXXX"
	_, err := c.ParseSessionCookie(tampered)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}
