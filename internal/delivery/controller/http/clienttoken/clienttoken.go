// Package clienttoken is the signed client cookie of DOS_PROTECTION=on: proof that this browser passed one
// bot check of the platform's provider. It carries a random id (the key of the browser's rate-limit bucket)
// and an expiry, signed with HMAC-SHA256. It is not a login and holds nothing about a person.
package clienttoken

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Cookie is the cookie name: __Host- like the session cookie (Secure, Path=/, no Domain).
const Cookie = "__Host-client"

// Path is the route that exchanges a provider token for the cookie.
const Path = "/api/client-token"

// RequiredHeader is set (to "required") on the 429 that refuses a request for lack of a valid client
// cookie, so the frontend fetches a token and retries; any other 429 is a rate limit proper.
const RequiredHeader = "X-Client-Token"

const (
	version = "v1"
	idBytes = 16
)

// Signer issues and checks client cookies.
type Signer struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewSigner signs with secret when it is set, else with a key derived from fallback (the platform's JWT
// signing secret, which every replica shares).
func NewSigner(secret, fallback string, ttl time.Duration) *Signer {
	key := []byte(secret)
	if secret == "" {
		mac := hmac.New(sha256.New, []byte(fallback))
		mac.Write([]byte("client-token"))
		key = mac.Sum(nil)
	}
	return &Signer{key: key, ttl: ttl, now: time.Now}
}

func (s *Signer) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

var enc = base64.RawURLEncoding

// Issue returns a new cookie value and its expiry.
func (s *Signer) Issue() (string, time.Time, error) {
	payload := make([]byte, idBytes+8)
	if _, err := rand.Read(payload[:idBytes]); err != nil {
		return "", time.Time{}, err
	}
	expires := s.now().Add(s.ttl)
	binary.BigEndian.PutUint64(payload[idBytes:], uint64(expires.Unix()))
	return version + "." + enc.EncodeToString(payload) + "." + enc.EncodeToString(s.sign(payload)), expires, nil
}

// Verify returns the id of the browser when the value is untampered and not expired.
func (s *Signer) Verify(value string) (string, bool) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 || parts[0] != version {
		return "", false
	}
	payload, err := enc.DecodeString(parts[1])
	if err != nil || len(payload) != idBytes+8 {
		return "", false
	}
	mac, err := enc.DecodeString(parts[2])
	if err != nil || !hmac.Equal(mac, s.sign(payload)) {
		return "", false
	}
	if int64(binary.BigEndian.Uint64(payload[idBytes:])) <= s.now().Unix() {
		return "", false
	}
	return string(payload[:idBytes]), true
}

// FromRequest reads the request's cookie and returns the browser id when it is valid.
func (s *Signer) FromRequest(ctx *gin.Context) (string, bool) {
	value, err := ctx.Cookie(Cookie)
	if err != nil || value == "" {
		return "", false
	}
	return s.Verify(value)
}

// Set issues a cookie on the response (HttpOnly, Secure, SameSite=Lax) and returns its expiry.
func (s *Signer) Set(ctx *gin.Context) (time.Time, error) {
	value, expires, err := s.Issue()
	if err != nil {
		return time.Time{}, err
	}
	ctx.SetSameSite(http.SameSiteLaxMode)
	ctx.SetCookie(Cookie, value, int(s.ttl.Seconds()), "/", "", true, true)
	return expires, nil
}
