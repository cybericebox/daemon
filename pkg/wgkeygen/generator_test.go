package wgkeygen

import (
	"encoding/base64"
	"testing"

	"golang.org/x/crypto/curve25519"
)

func TestNewKeyPair_ValidCurve25519(t *testing.T) {
	g := NewKeyGenerator()
	kp, err := g.NewKeyPair()
	if err != nil {
		t.Fatalf("NewKeyPair: %v", err)
	}

	priv, err := base64.StdEncoding.DecodeString(kp.PrivateKey)
	if err != nil || len(priv) != keySize {
		t.Fatalf("private key must be base64 of %d bytes: %v", keySize, err)
	}
	pub, err := base64.StdEncoding.DecodeString(kp.PublicKey)
	if err != nil || len(pub) != keySize {
		t.Fatalf("public key must be base64 of %d bytes: %v", keySize, err)
	}

	// Clamping per cr.yp.to/ecdh.html: low 3 bits of byte 0 cleared,
	// bit 7 of byte 31 cleared, bit 6 set.
	if priv[0]&7 != 0 || priv[31]&128 != 0 || priv[31]&64 != 64 {
		t.Fatalf("private key not clamped: first=%x last=%x", priv[0], priv[31])
	}

	// The public key must be the curve25519 base-point product of the private.
	var want, k [keySize]byte
	copy(k[:], priv)
	curve25519.ScalarBaseMult(&want, &k)
	if base64.StdEncoding.EncodeToString(want[:]) != kp.PublicKey {
		t.Fatal("public key does not match the private key")
	}
}

func TestNewPreSharedKey_UniqueAndSized(t *testing.T) {
	g := NewKeyGenerator()
	a, err := g.NewPreSharedKey()
	if err != nil {
		t.Fatalf("NewPreSharedKey: %v", err)
	}
	b, _ := g.NewPreSharedKey()
	if a == b {
		t.Fatal("two pre-shared keys must differ")
	}
	raw, err := base64.StdEncoding.DecodeString(a)
	if err != nil || len(raw) != keySize {
		t.Fatalf("psk must be base64 of %d bytes", keySize)
	}
}
