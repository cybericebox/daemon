package password_test

import (
	"errors"
	"testing"

	"github.com/cybericebox/daemon/pkg/password"
)

func defaultConfig() password.Config {
	return password.Config{
		HashCost: 4, // minimum cost — fast for tests
		Complexity: password.ComplexityConfig{
			MinLength:            8,
			MaxLength:            64,
			MinCapitalLetters:    1,
			MinSmallLetters:      1,
			MinDigits:            1,
			MinSpecialCharacters: 1,
		},
	}
}

func TestNew_DefaultCost(t *testing.T) {
	c := password.New(password.Config{}) // zero cost → default
	if c == nil {
		t.Fatal("expected non-nil client")
	}
}

// ── Hash / Matches ─────────────────────────────────────────────────────────

func TestHash_Matches_RoundTrip(t *testing.T) {
	c := password.New(defaultConfig())
	plain := "Secure!1"

	hashed, err := c.Hash(plain)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if hashed == plain {
		t.Fatal("hashed must differ from plaintext")
	}

	ok, err := c.Matches(plain, hashed)
	if err != nil {
		t.Fatalf("Matches: %v", err)
	}
	if !ok {
		t.Fatal("Matches must return true for correct password")
	}
}

func TestMatches_WrongPassword(t *testing.T) {
	c := password.New(defaultConfig())
	hashed, _ := c.Hash("Correct!1")

	ok, err := c.Matches("Wrong!1", hashed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatal("Matches must return false for wrong password")
	}
}

func TestMatches_InvalidHash(t *testing.T) {
	c := password.New(defaultConfig())
	_, err := c.Matches("anything", "not-a-valid-bcrypt-hash")
	if !errors.Is(err, password.ErrInvalidHash) {
		t.Fatalf("expected ErrInvalidHash, got %v", err)
	}
}

func TestHash_DifferentSalts(t *testing.T) {
	c := password.New(defaultConfig())
	h1, _ := c.Hash("Secure!1")
	h2, _ := c.Hash("Secure!1")
	if h1 == h2 {
		t.Fatal("same plaintext must produce different hashes (different salts)")
	}
}

// ── CheckPasswordComplexity ────────────────────────────────────────────────

func TestComplexity_Valid(t *testing.T) {
	c := password.New(defaultConfig())
	if err := c.CheckPasswordComplexity("Secure!1"); err != nil {
		t.Fatalf("expected nil for valid password, got %v", err)
	}
}

func TestComplexity_TooShort(t *testing.T) {
	c := password.New(defaultConfig())
	err := c.CheckPasswordComplexity("Sh!1")
	assertComplexityField(t, err, "minLength")
}

func TestComplexity_TooLong(t *testing.T) {
	c := password.New(defaultConfig())
	long := "Secure!1"
	for len(long) <= 64 {
		long += "a"
	}
	err := c.CheckPasswordComplexity(long)
	assertComplexityField(t, err, "maxLength")
}

func TestComplexity_NoCapital(t *testing.T) {
	c := password.New(defaultConfig())
	err := c.CheckPasswordComplexity("secure!1")
	assertComplexityField(t, err, "minCapitalLetters")
}

func TestComplexity_NoSmall(t *testing.T) {
	c := password.New(defaultConfig())
	err := c.CheckPasswordComplexity("SECURE!1")
	assertComplexityField(t, err, "minSmallLetters")
}

func TestComplexity_NoDigit(t *testing.T) {
	c := password.New(defaultConfig())
	err := c.CheckPasswordComplexity("Secure!!")
	assertComplexityField(t, err, "minDigits")
}

func TestComplexity_NoSpecial(t *testing.T) {
	c := password.New(defaultConfig())
	err := c.CheckPasswordComplexity("Secure11")
	assertComplexityField(t, err, "minSpecialCharacters")
}

func assertComplexityField(t *testing.T, err error, field string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected ComplexityError for field %q, got nil", field)
	}
	var ce *password.ComplexityError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ComplexityError, got %T: %v", err, err)
	}
	if ce.Field != field {
		t.Fatalf("expected Field=%q, got %q", field, ce.Field)
	}
}

// ── Complexity / SpecialCharacters ─────────────────────────────────────────

func TestComplexity_ReturnsConfiguredThresholds(t *testing.T) {
	cfg := defaultConfig()
	c := password.New(cfg)
	if got := c.Complexity(); got != cfg.Complexity {
		t.Fatalf("Complexity() = %+v, want %+v", got, cfg.Complexity)
	}
}

func TestSpecialCharacters_ExactSet(t *testing.T) {
	const want = "!\"#$%&'()*+,-./"
	if password.SpecialCharacters != want {
		t.Fatalf("SpecialCharacters = %q, want %q", password.SpecialCharacters, want)
	}
}

func TestSpecialCharacters_MatchComplexityCounting(t *testing.T) {
	c := password.New(password.Config{Complexity: password.ComplexityConfig{MinSpecialCharacters: 1}})
	for _, r := range password.SpecialCharacters {
		if err := c.CheckPasswordComplexity(string(r)); err != nil {
			t.Fatalf("%q listed as special but not counted: %v", r, err)
		}
	}
	for _, s := range []string{" ", ":", "@", "[", "_", "~", "a", "0"} {
		if err := c.CheckPasswordComplexity(s); err == nil {
			t.Fatalf("%q counted as special but not listed", s)
		}
	}
}
