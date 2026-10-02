// Package secret provides AES-256-GCM encryption of short string values
// (e.g. secret env-var values) for at-rest storage.
//
// A Cipher holds a keyring: the first key encrypts, every key opens. A ciphertext names the key that sealed it
// ("v1:<key id>:<base64>"), so a key can be replaced without touching what is already stored: put the new key
// first in the *_SECRETS_KEY list, keep the old one after it for as long as old values remain.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// versionPrefix marks a ciphertext that names its key. A value without it is the older plain base64 form,
// opened with each key of the ring in turn.
const versionPrefix = "v1:"

// defaultKeyID names a key given without an id (the single hex key of the old configuration).
const defaultKeyID = "default"

var keyIDPattern = regexp.MustCompile(`^[a-z0-9_-]{1,16}$`)

type ringKey struct {
	id  string
	key [32]byte
}

type Cipher struct {
	// keys[0] seals; all of them open.
	keys []ringKey
}

// Sealer is the encryption boundary used by higher-level secret envelope
// stores. Context is authenticated, never encrypted, and binds ciphertext to
// its intended purpose.
type Sealer interface {
	EncryptWithContext(plaintext, context []byte) (string, error)
	DecryptWithContext(encoded string, context []byte) ([]byte, error)
}

// New builds a Cipher from the value of a *_SECRETS_KEY setting: either one 64-char hex key (32 bytes, AES-256),
// or a comma-separated keyring of id:hex entries (an id is 1-16 characters of a-z, 0-9, _ and -), the first of
// which encrypts. The ring is how a key is rotated.
func New(spec string) (*Cipher, error) {
	entries := strings.Split(spec, ",")
	c := &Cipher{}
	seen := map[string]bool{}
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		id, hexKey := defaultKeyID, entry
		if before, after, ok := strings.Cut(entry, ":"); ok {
			id, hexKey = before, after
			if !keyIDPattern.MatchString(id) {
				return nil, errors.New("secret: a key id is 1-16 characters of a-z, 0-9, _ and -")
			}
		} else if len(entries) > 1 {
			return nil, errors.New("secret: every key of a keyring needs an id (id:hex)")
		}
		if seen[id] {
			return nil, fmt.Errorf("secret: key id %q is used twice", id)
		}
		seen[id] = true
		if len(hexKey) != 64 {
			return nil, errors.New("secret: key must be 64 hex chars (32 bytes)")
		}
		b, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, err
		}
		k := ringKey{id: id}
		copy(k.key[:], b)
		c.keys = append(c.keys, k)
	}
	return c, nil
}

// NewFromBytes builds a Cipher from an in-memory AES-256 key. Callers that
// generate per-record data keys can avoid turning those bytes into a string.
func NewFromBytes(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, errors.New("secret: key must be 32 bytes")
	}
	k := ringKey{id: defaultKeyID}
	copy(k.key[:], key)
	return &Cipher{keys: []ringKey{k}}, nil
}

// Destroy clears the in-memory keys. It is safe to call more than once.
func (c *Cipher) Destroy() {
	for i := range c.keys {
		for j := range c.keys[i].key {
			c.keys[i].key[j] = 0
		}
	}
}

// CurrentKeyID is the id of the key that encrypts.
func (c *Cipher) CurrentKeyID() string { return c.keys[0].id }

// NeedsRotation reports whether a stored ciphertext was not sealed by the current key (an older key, or the
// older form without a key id): it still opens, and sealing it again moves it to the current key.
func (c *Cipher) NeedsRotation(encoded string) bool {
	id, _, ok := splitCiphertext(encoded)
	return !ok || id != c.keys[0].id
}

// Encrypt returns v1:<key id>:base64( [nonce | ciphertext | tag] ).
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	return c.EncryptWithContext([]byte(plaintext), nil)
}

// EncryptWithContext seals plaintext with the current key, bound to authenticated context. A different context
// cannot decrypt the result.
func (c *Cipher) EncryptWithContext(plaintext, context []byte) (string, error) {
	gcm, err := newGCM(c.keys[0].key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, plaintext, context)
	return versionPrefix + c.keys[0].id + ":" + base64.StdEncoding.EncodeToString(sealed), nil
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
// context is provided. A ciphertext that names its key is opened with that key; an older one is tried against
// each key of the ring.
func (c *Cipher) DecryptWithContext(encoded string, context []byte) ([]byte, error) {
	if id, body, ok := splitCiphertext(encoded); ok {
		for _, k := range c.keys {
			if k.id == id {
				return open(k.key, body, context)
			}
		}
		return nil, fmt.Errorf("secret: the key %q that sealed this value is not in the keyring", id)
	}
	var lastErr error
	for _, k := range c.keys {
		plain, err := open(k.key, encoded, context)
		if err == nil {
			return plain, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// splitCiphertext reads v1:<id>:<base64>.
func splitCiphertext(encoded string) (id, body string, ok bool) {
	rest, found := strings.CutPrefix(encoded, versionPrefix)
	if !found {
		return "", "", false
	}
	id, body, found = strings.Cut(rest, ":")
	return id, body, found && id != ""
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func open(key [32]byte, encoded string, context []byte) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns+gcm.Overhead() {
		return nil, errors.New("secret: ciphertext too short")
	}
	return gcm.Open(nil, data[:ns], data[ns:], context)
}
