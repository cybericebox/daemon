package password

import (
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// ComplexityError is returned by CheckPasswordComplexity.
// Field indicates which rule was violated; Min is the required threshold.
type ComplexityError struct {
	Field string
	Min   int
}

func (e *ComplexityError) Error() string {
	return fmt.Sprintf("password: complexity rule violated: %s requires at least %d", e.Field, e.Min)
}

// SpecialCharacters is the exact set counted towards MinSpecialCharacters
// (ASCII 33–47). isSpecial is defined from it, so the published policy and the
// enforced rule cannot drift apart.
const SpecialCharacters = "!\"#$%&'()*+,-./"

var ErrInvalidHash = errors.New("password: invalid hashed password")

// ComplexityConfig defines password strength requirements.
type ComplexityConfig struct {
	MinLength            int
	MaxLength            int
	MinCapitalLetters    int
	MinSmallLetters      int
	MinDigits            int
	MinSpecialCharacters int
}

// Config holds bcrypt cost and complexity rules.
type Config struct {
	HashCost   int
	Complexity ComplexityConfig
}

// Client hashes passwords and checks complexity.
type Client struct {
	cost       int
	complexity ComplexityConfig
}

func New(cfg Config) *Client {
	cost := cfg.HashCost
	if cost == 0 {
		cost = bcrypt.DefaultCost
	}
	return &Client{cost: cost, complexity: cfg.Complexity}
}

// Complexity returns the active complexity thresholds.
func (c *Client) Complexity() ComplexityConfig {
	return c.complexity
}

// Hash bcrypt-hashes plaintext.
func (c *Client) Hash(plaintext string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plaintext), c.cost)
	if err != nil {
		return "", fmt.Errorf("password: hash: %w", err)
	}
	return string(b), nil
}

// Matches returns (false, nil) on mismatch, (true, nil) on match,
// and (false, ErrInvalidHash) when the stored hash is malformed.
func (c *Client) Matches(plaintext, hashed string) (bool, error) {
	err := bcrypt.CompareHashAndPassword([]byte(hashed), []byte(plaintext))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return false, nil
	}
	return false, ErrInvalidHash
}

// CheckPasswordComplexity returns a *ComplexityError describing the first
// violated rule, or nil when the password satisfies all requirements.
func (c *Client) CheckPasswordComplexity(password string) error {
	cc := c.complexity

	if len(password) < cc.MinLength {
		return &ComplexityError{Field: "minLength", Min: cc.MinLength}
	}
	if cc.MaxLength > 0 && len(password) > cc.MaxLength {
		return &ComplexityError{Field: "maxLength", Min: cc.MaxLength}
	}
	if countRune(password, isUpper) < cc.MinCapitalLetters {
		return &ComplexityError{Field: "minCapitalLetters", Min: cc.MinCapitalLetters}
	}
	if countRune(password, isLower) < cc.MinSmallLetters {
		return &ComplexityError{Field: "minSmallLetters", Min: cc.MinSmallLetters}
	}
	if countRune(password, isDigit) < cc.MinDigits {
		return &ComplexityError{Field: "minDigits", Min: cc.MinDigits}
	}
	if countRune(password, isSpecial) < cc.MinSpecialCharacters {
		return &ComplexityError{Field: "minSpecialCharacters", Min: cc.MinSpecialCharacters}
	}
	return nil
}

func countRune(s string, f func(rune) bool) int {
	n := 0
	for _, r := range s {
		if f(r) {
			n++
		}
	}
	return n
}

func isUpper(r rune) bool   { return r >= 'A' && r <= 'Z' }
func isLower(r rune) bool   { return r >= 'a' && r <= 'z' }
func isDigit(r rune) bool   { return r >= '0' && r <= '9' }
func isSpecial(r rune) bool { return strings.ContainsRune(SpecialCharacters, r) }
