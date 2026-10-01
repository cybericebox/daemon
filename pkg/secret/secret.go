// Package secret provides AES-256-GCM encryption of short string values
// (e.g. secret env-var values) for at-rest storage.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
)

type Cipher struct {
	key [32]byte
}

// Sealer is the encryption boundary used by higher-level secret envelope
// stores. Context is authenticated, never encrypted, and binds ciphertext to
// its intended purpose.
type Sealer interface {
	EncryptWithContext(plaintext, context []byte) (string, error)
	DecryptWithContext(encoded string, context []byte) ([]byte, error)
}

// New builds a Cipher from a 64-char hex key (32 bytes → AES-256).
func New(hexKey string) (*Cipher, error) {
	if len(hexKey) != 64 {
		return nil, errors.New("secret: key must be 64 hex chars (32 bytes)")
	}
	b, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, err
	}
	return NewFromBytes(b)
}

// NewFromBytes builds a Cipher from an in-memory AES-256 key. Callers that
// generate per-record data keys can avoid turning those bytes into a string.
func NewFromBytes(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("secret: key must be 32 bytes")
	}
	c := &Cipher{}
	copy(c.key[:], key)
	return c, nil
}

// Destroy clears the in-memory key. It is safe to call more than once.
func (c *Cipher) Destroy() {
	for i := range c.key {
		c.key[i] = 0
	}
}

// Encrypt returns base64( [nonce | ciphertext | tag] ).
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	return c.EncryptWithContext([]byte(plaintext), nil)
}

// EncryptWithContext returns base64([nonce | ciphertext | tag]) bound to
// authenticated context. A different context cannot decrypt the result.
func (c *Cipher) EncryptWithContext(plaintext, context []byte) (string, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, context)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Decrypt reverses Encrypt.
func (c *Cipher) Decrypt(encoded string) (string, error) {
	plain, err := c.DecryptWithContext(encoded, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

// DecryptWithContext reverses EncryptWithContext when the same authenticated
// context is provided.
func (c *Cipher) DecryptWithContext(encoded string, context []byte) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns+gcm.Overhead() {
		return nil, errors.New("secret: ciphertext too short")
	}
	plain, err := gcm.Open(nil, data[:ns], data[ns:], context)
	if err != nil {
		return nil, err
	}
	return plain, nil
}
