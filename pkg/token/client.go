package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/golang-jwt/jwt/v5"
)

const issuer = "id"

var (
	ErrInvalidToken = errors.New("token: invalid or malformed token")
	ErrEmptySignKey = errors.New("token: signing key cannot be empty")
)

// Config holds the secret needed by the token client.
type Config struct {
	TokenSignature string
	Issuer         string
	// SetupTokenTTL is the life of a setup link (SETUP_TOKEN_TTL); zero means SetupTokenTTL.
	SetupTokenTTL time.Duration
}

// Client signs and verifies JWTs.
type Client struct {
	signKey  []byte
	issuer   string
	setupTTL time.Duration
}

// New constructs a Client, returning an error on invalid config.
func New(cfg Config) (*Client, error) {
	if cfg.TokenSignature == "" {
		return nil, ErrEmptySignKey
	}
	if cfg.SetupTokenTTL < 0 {
		return nil, errors.New("token: the setup token ttl cannot be negative")
	}
	if cfg.SetupTokenTTL == 0 {
		cfg.SetupTokenTTL = SetupTokenTTL
	}
	return &Client{signKey: []byte(cfg.TokenSignature), issuer: cfg.Issuer, setupTTL: cfg.SetupTokenTTL}, nil
}

// MustNew is New but panics on error — safe to call at startup.
func MustNew(cfg Config) *Client {
	c, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return c
}

// ── Session cookie (the single platform credential, host-only on api.<domain>) ─

type sessionClaims struct {
	jwt.RegisteredClaims
}

// GenerateSessionCookie issues a signed JWT for the session. expiresAt is baked
// in so Stage-1 validation can reject expired cookies without a DB hit.
func (c *Client) GenerateSessionCookie(sessionID uuid.UUID, expiresAt time.Time) (string, error) {
	claims := sessionClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    c.issuer,
			Subject:   sessionID.String(),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ID:        uuid.Must(uuid.NewV7()).String(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(c.signKey)
	if err != nil {
		return "", fmt.Errorf("token: sign session cookie: %w", err)
	}
	return signed, nil
}

// ParseSessionCookie validates HMAC signature + expiry and returns the session ID.
// Session cookies carry no audience claim; tokens with an audience (e.g. a setup
// token) are rejected to prevent cross-purpose replay.
func (c *Client) ParseSessionCookie(tokenStr string) (uuid.UUID, error) {
	tok, err := jwt.ParseWithClaims(
		tokenStr,
		&sessionClaims{},
		c.keyFunc,
		jwt.WithIssuer(c.issuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	claims, ok := tok.Claims.(*sessionClaims)
	if !ok || !tok.Valid {
		return uuid.Nil, ErrInvalidToken
	}
	// Reject tokens that carry an audience claim — session cookies are unscoped.
	if len(claims.Audience) > 0 {
		return uuid.Nil, ErrInvalidToken
	}
	id, err := uuid.FromString(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	return id, nil
}

func (c *Client) keyFunc(t *jwt.Token) (interface{}, error) {
	if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
	}
	return c.signKey, nil
}
