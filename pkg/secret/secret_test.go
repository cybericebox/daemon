package secret_test

import (
	"strings"
	"testing"

	"github.com/cybericebox/daemon/pkg/secret"
)

const testKey = "6368616e676520746869732070617373776f726420746f206120736563726574"

func TestCipher_RoundTrip(t *testing.T) {
	c, err := secret.New(testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ct, err := c.Encrypt("s3cret-value")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if ct == "s3cret-value" || ct == "" {
		t.Fatalf("ciphertext must differ from plaintext, got %q", ct)
	}
	pt, err := c.Decrypt(ct)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if pt != "s3cret-value" {
		t.Fatalf("round-trip mismatch: %q", pt)
	}
}

func TestCipher_NonDeterministicNonce(t *testing.T) {
	c, _ := secret.New(testKey)
	a, _ := c.Encrypt("same")
	b, _ := c.Encrypt("same")
	if a == b {
		t.Fatal("two encryptions of the same value must differ (random nonce)")
	}
}

func TestNew_RejectsBadKey(t *testing.T) {
	if _, err := secret.New("deadbeef"); err == nil {
		t.Fatal("short key must be rejected")
	}
	if _, err := secret.New(strings.Repeat("zz", 32)); err == nil {
		t.Fatal("non-hex key must be rejected")
	}
}

func TestDecrypt_RejectsGarbage(t *testing.T) {
	c, _ := secret.New(testKey)
	if _, err := c.Decrypt("bm90LWEtY2lwaGVydGV4dA=="); err == nil {
		t.Fatal("garbage ciphertext must be rejected")
	}
}

func TestCipher_AuthenticatedContextMustMatch(t *testing.T) {
	c, err := secret.New(testKey)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	encoded, err := c.EncryptWithContext([]byte("token"), []byte("signal:invitation.sent:token"))
	if err != nil {
		t.Fatalf("EncryptWithContext: %v", err)
	}

	plain, err := c.DecryptWithContext(encoded, []byte("signal:invitation.sent:token"))
	if err != nil {
		t.Fatalf("DecryptWithContext: %v", err)
	}
	if string(plain) != "token" {
		t.Fatalf("round-trip mismatch: %q", plain)
	}

	if _, err := c.DecryptWithContext(encoded, []byte("signal:password.reset:token")); err == nil {
		t.Fatal("decrypting with different authenticated context must fail")
	}
}
