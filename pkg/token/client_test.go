package token_test

import (
	"testing"


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
