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
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode"
)

// versionPrefix marks a ciphertext that names its key. A value without it is the older plain base64 form,
// opened with each key of the ring in turn.
const versionPrefix = "v1:"

// defaultKeyID names a key given without an id (the single hex key of the old configuration).
const defaultKeyID = "default"

// The classes of a failed open. Messages carry no value, only the class (and a key id for the first).
var (
	// ErrUnknownKey: the key that sealed the value is not in the ring.
	ErrUnknownKey = errors.New("secret: the key that sealed this value is not in the keyring")
	// ErrCorrupt: the stored text is not a ciphertext of this package (bad encoding, too short).
	ErrCorrupt = errors.New("secret: stored value is not a valid ciphertext")
	// ErrAuth: the key is right or tried, but authentication failed (other context, wrong key, damaged value).
	ErrAuth = errors.New("secret: authentication failed")
)

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

// DeriveKeys returns purpose-separated HMAC-SHA256 keys in keyring order:
// the first is current, the remainder are verification-only rotation keys.
// Names do not affect derivation, so naming an existing raw key preserves it.
// Callers use a fixed versioned purpose; raw AES material is never exposed.
func (c *Cipher) DeriveKeys(purpose string) [][32]byte {
	derived := make([][32]byte, len(c.keys))
	for i, k := range c.keys {
		mac := hmac.New(sha256.New, k.key[:])
		mac.Write([]byte(purpose))
		copy(derived[i][:], mac.Sum(nil))
	}
	return derived
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

// KeyIDs lists the ids of the keys of the ring, the sealing key first.
func (c *Cipher) KeyIDs() []string {
	ids := make([]string, len(c.keys))
	for i, k := range c.keys {
		ids[i] = k.id
	}
	return ids
}

// HasKey reports whether the ring holds the key with this id.
func (c *Cipher) HasKey(id string) bool {
	for _, k := range c.keys {
		if k.id == id {
			return true
		}
	}
	return false
}

// SafeKeyID returns the key id for display: an id read from stored data that does not have the shape of a key id is
// replaced, so nothing from the database can put control characters or long text into an operator's terminal.
func SafeKeyID(id string) string {
	if keyIDPattern.MatchString(id) {
		return id
	}
	return "invalid-id"
}

// Printable returns s for display, with every non-printable rune replaced and the text cut to max runes. Stored text
// (names inside a document) goes through it before it reaches output.
func Printable(s string, max int) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if len(out) == max {
			out = append(out, '…')
			break
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) || !unicode.IsPrint(r) {
			r = '?'
		}
		out = append(out, r)
	}
	return string(out)
}

// KeyID reads which key sealed a stored ciphertext without opening it. legacy is true for the older form that names
// no key (and for anything that is not a ciphertext of this package).
func KeyID(encoded string) (id string, legacy bool) {
	id, _, ok := splitCiphertext(encoded)
	return id, !ok
}

// NeedsRotation reports whether a stored ciphertext was not sealed by the current key (an older key, or the
// older form without a key id): it still opens, and sealing it again moves it to the current key.
func (c *Cipher) NeedsRotation(encoded string) bool {
	id, _, ok := splitCiphertext(encoded)
	return !ok || id != c.keys[0].id
}

// ErrNoContext: a value was sealed or opened without a context. Every secret is bound to the row or purpose it belongs to
// (see internal/secretctx), so there is no way to seal without one.
var ErrNoContext = errors.New("secret: a context is required")

// EncryptWithContext seals plaintext with the current key, bound to authenticated context, as
// v1:<key id>:base64( [nonce | ciphertext | tag] ). A different context cannot decrypt the result, and an empty one is
// refused (ErrNoContext).
func (c *Cipher) EncryptWithContext(plaintext, context []byte) (string, error) {
	if len(context) == 0 {
		return "", ErrNoContext
	}
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

// DecryptWithContext reverses EncryptWithContext when the same authenticated
// context is provided. A ciphertext that names its key is opened with that key; an older one is tried against
// each key of the ring.
func (c *Cipher) DecryptWithContext(encoded string, context []byte) ([]byte, error) {
	if len(context) == 0 {
		return nil, ErrNoContext
	}
	if id, body, ok := splitCiphertext(encoded); ok {
		for _, k := range c.keys {
			if k.id == id {
				return open(k.key, body, context)
			}
		}
		return nil, fmt.Errorf("%w: %q", ErrUnknownKey, id)
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
		return nil, ErrCorrupt
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	ns := gcm.NonceSize()
	if len(data) < ns+gcm.Overhead() {
		return nil, ErrCorrupt
	}
	plain, err := gcm.Open(nil, data[:ns], data[ns:], context)
	if err != nil {
		return nil, ErrAuth
	}
	return plain, nil
}
