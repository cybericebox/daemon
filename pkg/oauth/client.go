package oauth

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/oauth2"
)

const (
	stateSubject = "oauth-state"
	stateIssuer  = "oauth"
)

var (
	ErrEmptyStateSignature = errors.New("oauth: state signature cannot be empty")
	ErrInvalidState        = errors.New("oauth: invalid or expired oauth state")
)

// Config holds OAuth provider settings.
type Config struct {
	Google              ClientConfig
	RedirectURLTemplate string // e.g. "https://api.example.com/api/auth/%s/callback"
	StateSignature      string
	StateTTL            time.Duration
}

// ClientConfig holds Google OAuth2 app credentials.
type ClientConfig struct {
	ClientID     string
	ClientSecret string
}

// Client manages Google OAuth2 flow.
type Client struct {
	googleCfg *oauth2.Config
	signKey   []byte
	stateTTL  time.Duration
}

// New constructs a Client, returning an error on invalid config.
func New(cfg Config) (*Client, error) {
	if cfg.StateSignature == "" {
		return nil, ErrEmptyStateSignature
	}
	ttl := cfg.StateTTL
	if ttl == 0 {
		ttl = 10 * time.Minute
	}
	return &Client{
		googleCfg: newGoogleClientConfig(cfg.Google, cfg.RedirectURLTemplate),
		signKey:   []byte(cfg.StateSignature),
		stateTTL:  ttl,
	}, nil
}

// ValidateState checks that a state token is signed by this client and not expired.
func (c *Client) ValidateState(state string) error {
	_, err := c.parseStateToken(state)
	return err
}

// ── State JWT helpers ─────────────────────────────────────────────────────────

type stateClaims struct {
	jwt.RegisteredClaims
	// Redirect is the post-auth landing URL, embedded here (rather than carried
	// in a cookie) because it must survive an external round-trip to Google
	// that we do not control — the state token is the only thing guaranteed to
	// come back on the callback.
	Redirect string `json:"redirect,omitempty"`
}

func (c *Client) newStateToken(redirect string) (string, error) {
	now := time.Now()
	claims := stateClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    stateIssuer,
			Subject:   stateSubject,
			ExpiresAt: jwt.NewNumericDate(now.Add(c.stateTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		Redirect: redirect,
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return tok.SignedString(c.signKey)
}

func (c *Client) parseStateToken(tokenStr string) (*stateClaims, error) {
	tok, err := jwt.ParseWithClaims(
		tokenStr,
		&stateClaims{},
		func(t *jwt.Token) (any, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected alg: %v", t.Header["alg"])
			}
			return c.signKey, nil
		},
		jwt.WithIssuer(stateIssuer),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return nil, ErrInvalidState
	}
	claims, ok := tok.Claims.(*stateClaims)
	if !ok || !tok.Valid || claims.Subject != stateSubject {
		return nil, ErrInvalidState
	}
	return claims, nil
}
