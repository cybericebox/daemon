package token

import (
	"errors"
	"fmt"
	"time"

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

func (c *Client) keyFunc(t *jwt.Token) (interface{}, error) {
	if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
		return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
	}
	return c.signKey, nil
}
