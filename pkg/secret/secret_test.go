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

const (
	oldKey = "6368616e676520746869732070617373776f726420746f206120736563726574"
	newKey = "0001020304050607080910111213141516171819202122232425262728293031"
)

func TestKeyringSealsWithTheFirstKeyAndOpensWithAny(t *testing.T) {
	old, err := secret.New("old:" + oldKey)
	if err != nil {
		t.Fatal(err)
	}
	sealedByOld, _ := old.Encrypt("kept")
	if !strings.HasPrefix(sealedByOld, "v1:old:") {
		t.Fatalf("a ciphertext names its key: %q", sealedByOld)
	}
	// The key is replaced: the new one first, the old one kept after it.
	rotated, err := secret.New("new:" + newKey + ", old:" + oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if plain, err := rotated.Decrypt(sealedByOld); err != nil || plain != "kept" {
		t.Fatalf("a value of the old key must still open: %q %v", plain, err)
	}
	fresh, _ := rotated.Encrypt("fresh")
	if !strings.HasPrefix(fresh, "v1:new:") || rotated.CurrentKeyID() != "new" {
		t.Fatalf("the first key seals: %q", fresh)
	}
	if !rotated.NeedsRotation(sealedByOld) || rotated.NeedsRotation(fresh) {
		t.Fatal("only the value of the old key needs sealing again")
	}
	// Once the old key is gone from the ring its values no longer open, and the error says which key is missing.
	only, _ := secret.New("new:" + newKey)
	if _, err = only.Decrypt(sealedByOld); err == nil || !strings.Contains(err.Error(), `"old"`) {
		t.Fatalf("a key missing from the ring: %v", err)
	}
	if plain, err := only.Decrypt(fresh); err != nil || plain != "fresh" {
		t.Fatal("the new key opens its own values")
	}
}

func TestAnOlderValueWithoutAKeyIdOpensWithTheRing(t *testing.T) {
	// The previous format: plain base64 of nonce|ciphertext|tag, sealed by the single key of the old setup.
	c, _ := secret.New("new:" + newKey + ",old:" + oldKey)
	single, _ := secret.New(oldKey)
	ct, _ := single.Encrypt("legacy-value")
	bare := strings.TrimPrefix(ct, "v1:default:")
	if plain, err := c.Decrypt(bare); err != nil || plain != "legacy-value" {
		t.Fatalf("a value without a key id opens with the keys of the ring: %q %v", plain, err)
	}
	if !c.NeedsRotation(bare) {
		t.Fatal("an older value should be sealed again")
	}
}

func TestKeyringSyntaxIsValidated(t *testing.T) {
	for name, spec := range map[string]string{
		"two keys without ids": oldKey + "," + newKey,
		"a duplicate id":       "a:" + oldKey + ",a:" + newKey,
		"a bad id":             "UPPER:" + oldKey,
		"an id too long":       strings.Repeat("a", 17) + ":" + oldKey,
		"a short key":          "a:deadbeef",
		"empty":                "",
	} {
		if _, err := secret.New(spec); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}
