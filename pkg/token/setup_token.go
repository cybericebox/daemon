package token

import (
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/golang-jwt/jwt/v5"
)

// SetupTokenTTL is the default lifetime (SETUP_TOKEN_TTL overrides it) of a setup link: the one validity of every
// account setup and invitation link (event and platform invitations, sign-up).
// It stays well under the 30-day retention of unconfirmed accounts, so a
// freshly (re)sent link always outlives the account it sets up.
//
// Single-use semantics are enforced upstream by account lifecycle — once the
// account transitions to status='active', the setup endpoints reject the request
// regardless of whether the token is still cryptographically valid. There is no
// server-side token store; revocation is implicit through the account state machine.
const SetupTokenTTL = 7 * 24 * time.Hour

// setupAudience is the fixed audience claim that distinguishes setup tokens from
// session cookies (no aud) and subdomain tokens (aud = subdomain name).
// ParseSetupToken requires this audience, so a setup token cannot be replayed
// through ParseToken or ParseSessionCookie.
const setupAudience = "setup"

type setupClaims struct {
	jwt.RegisteredClaims
}

// GenerateSetupToken issues a short-lived signed JWT containing the userID.
// The token is scoped to the "setup" audience so it cannot be accepted by
// ParseToken (subdomain tokens) or ParseSessionCookie (no-audience session cookies).
func (c *Client) GenerateSetupToken(userID uuid.UUID) (string, error) {
	now := time.Now()
	claims := setupClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   userID.String(),
			Audience:  jwt.ClaimStrings{setupAudience},
			ExpiresAt: jwt.NewNumericDate(now.Add(c.setupTTL)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        uuid.Must(uuid.NewV7()).String(),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := tok.SignedString(c.signKey)
	if err != nil {
		return "", fmt.Errorf("token: sign setup token: %w", err)
	}
	return signed, nil
}

// ParseSetupToken validates the HMAC signature, audience ("setup"), and expiry,
// then returns the userID encoded in the Subject claim.
// A tampered, expired, or incorrectly-typed token returns ErrInvalidToken.
func (c *Client) ParseSetupToken(tokenStr string) (uuid.UUID, error) {
	tok, err := jwt.ParseWithClaims(
		tokenStr,
		&setupClaims{},
		c.keyFunc,
		jwt.WithIssuer(issuer),
		jwt.WithAudience(setupAudience),
		jwt.WithExpirationRequired(),
	)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	claims, ok := tok.Claims.(*setupClaims)
	if !ok || !tok.Valid {
		return uuid.Nil, ErrInvalidToken
	}
	id, err := uuid.FromString(claims.Subject)
	if err != nil {
		return uuid.Nil, ErrInvalidToken
	}
	return id, nil
}
