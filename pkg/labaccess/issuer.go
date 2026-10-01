// Package labaccess signs the lab access tokens of the laboratory L7 proxy: the
// handoff links. The platform sets no cookie on the lab domain (the API and the
// labs live on different domains): the task page asks for a link per device and
// opens https://<device>-<code>.<base>/_auth?t=<jwt>. The proxy verifies the
// EdDSA (Ed25519) signature offline, then sets its own cookie and redirects to
// the site root. The proxy holds only the public keys of the tenant: every
// infrastructure agent has its own tenant and its own signing key, so a token
// is signed with the key of the agent that holds the lab group (iss = the tenant
// name, kid = the key id, aud = laboratory-proxy).
//
// A token carries what the operator knows and nothing else: the lab group
// (group_id, also the namespace), the LabGroupClient of that group (client, the
// same object that is the VPN peer), the device host label the link is for
// (host), the end of the session the proxy cookie gets (sess: the event's
// effective finish plus a buffer, or a test deploy's lease end; DefaultSessionTTL
// is the fallback), and iat, exp and a version. The token itself lives about a
// minute (LAB_ACCESS_TOKEN_TTL, five at most) and is stateless: nothing is
// remembered about it on either side. The proxy authorizes every request by the
// group access policy, so blocking a client refuses a still valid session.
// Clients are created by the platform when a team is formed or a member is
// added, never while issuing a link.
package labaccess

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// DefaultSessionTTL is the session length when the caller names no end (an
	// event without a finish).
	DefaultSessionTTL = 24 * time.Hour
	// DefaultTokenTTL is how long an access token can be opened.
	DefaultTokenTTL = time.Minute
	// MaxTokenTTL caps LAB_ACCESS_TOKEN_TTL; the proxy refuses a longer token too.
	MaxTokenTTL = 5 * time.Minute
	// AuthPath is the proxy path that consumes a handoff link.
	AuthPath = "/_auth"
)

// Audience is the token's aud: the proxy accepts only tokens meant for it.
const Audience = "laboratory-proxy"

// Config sets how long a token can be opened.
type Config struct {
	// TokenTTL is how long a token can be opened (default one minute, five at most).
	TokenTTL time.Duration
}

// SigningKey is the key of one tenant that signs its access tokens: the tenant name is the issuer, KeyID
// the key id the proxy picks the verifying key by (the token's kid header).
type SigningKey struct {
	Tenant string
	KeyID  string
	Key    ed25519.PrivateKey
}

// Session is who the link is for, in the operator's own terms: a LabGroup and
// one LabGroupClient of that group. The client is the same object that is the
// participant's VPN peer, so blocking it in the group access policy blocks the
// VPN and the web together. The platform knows which event, team or test deploy
// a group belongs to and which user a client is; the token does not say.
type Session struct {
	// Group is the lab group name (e-<event>-t-<team> or t-<deploy>), which is
	// also its namespace.
	Group string
	// Client is the LabGroupClient name inside the group (p-<user id>).
	Client string
	// AccessURL is the device URL from the lab status (https://<device>-<code>.<base>/...).
	// The link is built on its origin; its first host label is the token's host.
	AccessURL string
	// ExpiresAt is the wanted end of the session (the event's effective finish
	// plus a buffer, or a test deploy's lease end). Zero, or a time that is not in
	// the future, means the fallback TTL.
	ExpiresAt time.Time
}

// Link is a signed handoff link.
type Link struct {
	// URL is the full link to open in the browser.
	URL string
	// Token is the signed JWT inside the link.
	Token string
	// ExpiresAt is the end of the session the proxy will grant.
	ExpiresAt time.Time
}

// claims: iss = the tenant, sub = the LabGroupClient, aud = laboratory-proxy, iat/nbf/exp and
// group_id (the lab group id), host (<device>-<code>) and sess (the unix end of the session).
type claims struct {
	GroupID string `json:"group_id"`
	Host    string `json:"host"`
	Session int64  `json:"sess"`
	jwt.RegisteredClaims
}

type Issuer struct {
	ttl time.Duration
}

// New returns an issuer; the signing key is given per token.
func New(cfg Config) (*Issuer, error) {
	switch {
	case cfg.TokenTTL == 0:
		cfg.TokenTTL = DefaultTokenTTL
	case cfg.TokenTTL < 0 || cfg.TokenTTL > MaxTokenTTL:
		return nil, fmt.Errorf("labaccess: the token ttl must be up to %s", MaxTokenTTL)
	}
	return &Issuer{ttl: cfg.TokenTTL}, nil
}

// Issue signs an access link for one device with the tenant's key, valid for the token TTL from now.
func (i *Issuer) Issue(sk SigningKey, s Session, now time.Time) (Link, error) {
	if sk.Tenant == "" || sk.KeyID == "" || len(sk.Key) != ed25519.PrivateKeySize {
		return Link{}, errors.New("labaccess: no signing key")
	}
	if s.Group == "" || s.Client == "" {
		return Link{}, errors.New("labaccess: incomplete session")
	}
	origin, err := url.Parse(s.AccessURL)
	if err != nil || origin.Scheme == "" || origin.Hostname() == "" {
		return Link{}, errors.New("labaccess: the device has no web address")
	}
	host := strings.SplitN(origin.Hostname(), ".", 2)[0]
	end := now.Add(DefaultSessionTTL)
	if s.ExpiresAt.After(now) {
		end = s.ExpiresAt
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims{
		GroupID: s.Group, Host: host, Session: end.Unix(),
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: sk.Tenant, Subject: s.Client, Audience: jwt.ClaimStrings{Audience},
			IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(i.ttl)),
		},
	})
	token.Header["kid"] = sk.KeyID
	signed, err := token.SignedString(sk.Key)
	if err != nil {
		return Link{}, fmt.Errorf("sign lab access token: %w", err)
	}
	link := url.URL{Scheme: origin.Scheme, Host: origin.Host, Path: AuthPath, RawQuery: url.Values{"t": {signed}}.Encode()}
	return Link{URL: link.String(), Token: signed, ExpiresAt: end}, nil
}
