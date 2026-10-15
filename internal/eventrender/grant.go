// Package eventrender signs only short-lived, event-host-bound common-page reads.
// Its claims deliberately are not platform session claims.
package eventrender

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/gofrs/uuid"
	"github.com/golang-jwt/jwt/v5"
)

const Type = "cib-event-render+jwt"
const Issuer = "cybericebox:event-render"
const Scope = "common-pages"
const MaxLifetime = 60 * time.Second
const maxTokenBytes = 4096

var errInvalid = errors.New("invalid event render grant")

type Claims struct {
	jwt.RegisteredClaims
	SessionID string `json:"sid"`
	EventID   string `json:"event_id"`
	Scope     string `json:"scope"`
	Preview   bool   `json:"preview"`
	Challenge string `json:"challenge"`
}

// The wire contract uses one origin string, never a JWT audience array.
func (c Claims) MarshalJSON() ([]byte, error) {
	type wire Claims
	if len(c.Audience) != 1 {
		return nil, errInvalid
	}
	return json.Marshal(struct {
		wire
		Audience string `json:"aud"`
	}{wire(c), c.Audience[0]})
}
func (c *Claims) UnmarshalJSON(data []byte) error {
	type wire Claims
	var decoded struct {
		wire
		Audience string `json:"aud"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*c = Claims(decoded.wire)
	c.Audience = jwt.ClaimStrings{decoded.Audience}
	return nil
}

type Key struct {
	KeyID     string `json:"KeyID"`
	PublicKey string `json:"PublicKey"`
}
type Signer struct {
	key          ed25519.PrivateKey
	public       Key
	verification map[string]ed25519.PublicKey
}

// New purpose-derives a stable Ed25519 seed from the existing AES session key.
// HMAC's fixed versioned domain ensures this seed cannot be used as the sealing key.
func New(sessionKey string) (*Signer, error) {
	ring, err := secret.New(sessionKey)
	if err != nil {
		return nil, err
	}
	defer ring.Destroy()
	s := &Signer{verification: make(map[string]ed25519.PublicKey)}
	for i, seed := range ring.DeriveKeys("cybericebox:event-render:ed25519:v1") {
		key := ed25519.NewKeyFromSeed(seed[:])
		pub := key.Public().(ed25519.PublicKey)
		id := sha256.Sum256(pub)
		kid := base64.RawURLEncoding.EncodeToString(id[:16])
		s.verification[kid] = pub
		if i == 0 {
			s.key = key
			s.public = Key{KeyID: kid, PublicKey: base64.RawURLEncoding.EncodeToString(pub)}
		}
	}
	return s, nil
}
func (s *Signer) PublicKey() Key { return s.public }
func ValidChallenge(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(value) == 43 && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}
func validOrigin(origin string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.Path == "" && u.RawQuery == "" && u.Fragment == "" && origin == "https://"+strings.ToLower(u.Host)
}
func (s *Signer) Issue(audience string, eventID, userID, sessionID uuid.UUID, preview bool, challenge string, now time.Time) (string, int64, error) {
	if !validOrigin(audience) || eventID == uuid.Nil || userID == uuid.Nil || sessionID == uuid.Nil || !ValidChallenge(challenge) {
		return "", 0, errInvalid
	}
	now = now.Truncate(time.Second)
	exp := now.Add(MaxLifetime)
	claims := Claims{RegisteredClaims: jwt.RegisteredClaims{Issuer: Issuer, Subject: userID.String(), Audience: jwt.ClaimStrings{audience}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(exp)}, SessionID: sessionID.String(), EventID: eventID.String(), Scope: Scope, Preview: preview, Challenge: challenge}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = s.public.KeyID
	token.Header["typ"] = Type
	signed, err := token.SignedString(s.key)
	return signed, exp.Unix(), err
}
func (s *Signer) Verify(value, audience string, eventID uuid.UUID, now time.Time) (Claims, error) {
	if len(value) > maxTokenBytes || !validOrigin(audience) {
		return Claims{}, errInvalid
	}
	claims := Claims{}
	token, err := jwt.ParseWithClaims(value, &claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodEdDSA || token.Header["typ"] != Type || token.Header["kid"] == nil {
			return nil, errInvalid
		}
		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, errInvalid
		}
		key, ok := s.verification[kid]
		if !ok {
			return nil, errInvalid
		}
		return key, nil
	}, jwt.WithValidMethods([]string{"EdDSA"}), jwt.WithIssuer(Issuer), jwt.WithAudience(audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil || !token.Valid || claims.IssuedAt == nil || claims.ExpiresAt == nil {
		return Claims{}, errInvalid
	}
	if claims.Scope != Scope || claims.EventID != eventID.String() || len(claims.Audience) != 1 || claims.Audience[0] != audience || !ValidChallenge(claims.Challenge) || !claims.ExpiresAt.After(claims.IssuedAt.Time) || claims.ExpiresAt.Sub(claims.IssuedAt.Time) > MaxLifetime {
		return Claims{}, errInvalid
	}
	for _, v := range []string{claims.Subject, claims.SessionID, claims.EventID} {
		id, e := uuid.FromString(v)
		if e != nil || id == uuid.Nil || id.String() != v {
			return Claims{}, errInvalid
		}
	}
	return claims, nil
}
