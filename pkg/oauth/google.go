package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	googleUserInfoURL = "https://www.googleapis.com/oauth2/v2/userinfo"
)

var (
	ErrGoogleUserFetch = errors.New("oauth: failed to fetch google user info")
	// ErrGoogleEmailNotVerified: Google does not vouch for the address, so it
	// proves nothing about who owns the mailbox and must never be linked to an account.
	ErrGoogleEmailNotVerified = errors.New("oauth: google email is not verified")
)

// GoogleUser holds the profile data returned by Google.
type GoogleUser struct {
	GoogleID string
	Email    string
	// EmailVerified is always true for a user the client returns: unverified
	// profiles are rejected while parsing.
	EmailVerified bool
	Name          string // full display name ("name")
	GivenName     string // "given_name"
	FamilyName    string // "family_name"
	Picture       string
}

// FirstLastName returns the user's first and last name: given_name/family_name
// when Google provides a given name, otherwise "name" split on the first space
// (first token → first name, the rest → last name).
func (u GoogleUser) FirstLastName() (first, last string) {
	if given := strings.TrimSpace(u.GivenName); given != "" {
		return given, strings.TrimSpace(u.FamilyName)
	}
	first, last, _ = strings.Cut(strings.TrimSpace(u.Name), " ")
	return first, strings.TrimSpace(last)
}

func newGoogleClientConfig(cfg ClientConfig, redirectTemplate string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		Endpoint:     google.Endpoint,
		RedirectURL:  fmt.Sprintf(redirectTemplate, "google"),
		Scopes: []string{
			"https://www.googleapis.com/auth/userinfo.profile",
			"https://www.googleapis.com/auth/userinfo.email",
		},
	}
}

// GetGoogleLoginURL generates a signed state token — embedding redirect so it
// survives the round-trip to Google without a cookie — and returns the Google
// consent URL.
func (c *Client) GetGoogleLoginURL(redirect string) (loginURL string, state string, err error) {
	state, err = c.newStateToken(redirect)
	if err != nil {
		return "", "", fmt.Errorf("oauth: generate state: %w", err)
	}
	return c.googleCfg.AuthCodeURL(state), state, nil
}

// GetGoogleUser validates state, exchanges code for tokens, fetches the Google
// user profile, and returns the redirect embedded in the state at start time.
func (c *Client) GetGoogleUser(
	ctx context.Context,
	code, state string,
) (*GoogleUser, string, error) {
	claims, err := c.parseStateToken(state)
	if err != nil {
		return nil, "", err
	}
	if !c.consumeState(claims) {
		return nil, "", ErrInvalidState
	}
	tokens, err := c.googleCfg.Exchange(ctx, code)
	if err != nil {
		return nil, "", fmt.Errorf("oauth: exchange code: %w", err)
	}
	user, err := c.fetchGoogleUser(ctx, googleUserInfoURL, tokens)
	if err != nil {
		return nil, "", err
	}
	return user, claims.Redirect, nil
}

// GetGoogleUserFromToken is a testable variant that accepts a pre-exchanged token and a custom userinfo URL.
// Use in tests to inject a mock server URL and token without a real OAuth2 round-trip.
func (c *Client) GetGoogleUserFromToken(
	ctx context.Context,
	state, userInfoURL string,
	tokens *oauth2.Token,
) (*GoogleUser, string, error) {
	claims, err := c.parseStateToken(state)
	if err != nil {
		return nil, "", err
	}
	if !c.consumeState(claims) {
		return nil, "", ErrInvalidState
	}
	user, err := c.fetchGoogleUser(ctx, userInfoURL, tokens)
	if err != nil {
		return nil, "", err
	}
	return user, claims.Redirect, nil
}

func (c *Client) fetchGoogleUser(
	ctx context.Context,
	userInfoURL string,
	tokens *oauth2.Token,
) (*GoogleUser, error) {
	httpClient := c.googleCfg.Client(ctx, tokens)
	resp, err := httpClient.Get(userInfoURL)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrGoogleUserFetch, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read body: %w", ErrGoogleUserFetch, err)
	}
	return parseGoogleUserResponse(body)
}

// parseGoogleUserResponse decodes a Google userinfo JSON payload.
func parseGoogleUserResponse(body []byte) (*GoogleUser, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%w: unmarshal: %w", ErrGoogleUserFetch, err)
	}
	get := func(key string) string {
		if v, ok := raw[key].(string); ok {
			return v
		}
		return ""
	}
	// userinfo v2 reports "verified_email", OIDC "email_verified"; only a JSON
	// boolean true counts.
	verified := raw["verified_email"] == true || raw["email_verified"] == true
	u := &GoogleUser{
		GoogleID:      get("id"),
		Email:         get("email"),
		EmailVerified: verified,
		Name:          get("name"),
		GivenName:     get("given_name"),
		FamilyName:    get("family_name"),
		Picture:       get("picture"),
	}
	if u.GoogleID == "" || u.Email == "" {
		return nil, fmt.Errorf("%w: missing required fields", ErrGoogleUserFetch)
	}
	if !verified {
		return nil, ErrGoogleEmailNotVerified
	}
	return u, nil
}
