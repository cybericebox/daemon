package app

import (
	"strings"
	"testing"
)

func TestNewCipher(t *testing.T) {
	if c, err := newCipher(""); c != nil || err != nil {
		t.Fatalf("an unset key disables the feature without an error: %v %v", c, err)
	}
	if _, err := newCipher("not-hex"); err == nil {
		t.Fatal("an invalid key must be an error (fatal at start-up)")
	}
	c, err := newCipher(strings.Repeat("ab", 32))
	if err != nil || c == nil {
		t.Fatalf("a valid key builds a cipher: %v", err)
	}
}

// Each family has its own key: what one cipher sealed the other cannot open.
func TestNewCipher_KeysAreIndependent(t *testing.T) {
	vpn, _ := newCipher(strings.Repeat("ab", 32))
	exercise, _ := newCipher(strings.Repeat("cd", 32))
	sealed, err := vpn.Encrypt("wg-config")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exercise.Decrypt(sealed); err == nil {
		t.Fatal("another family's key must not open a VPN config")
	}
	if got, err := vpn.Decrypt(sealed); err != nil || got != "wg-config" {
		t.Fatalf("own key opens it: %q %v", got, err)
	}
}
