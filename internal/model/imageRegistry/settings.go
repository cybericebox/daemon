package imageRegistryModel

import (
	"strings"
	"time"
)

// MaxImageArchiveBytes is the upload limit for one image archive (10 GiB).
const MaxImageArchiveBytes = 10 << 30

type Provider string

const (
	ProviderDockerHub Provider = "dockerhub"
	ProviderGHCR      Provider = "ghcr"
)

func (p Provider) Valid() bool {
	return p == ProviderDockerHub || p == ProviderGHCR
}

// RegistryHost is the host the registry API is spoken to.
func (p Provider) RegistryHost() string {
	switch p {
	case ProviderDockerHub:
		return "index.docker.io"
	case ProviderGHCR:
		return "ghcr.io"
	}
	return ""
}

// RefHost is the host used in image references.
func (p Provider) RefHost() string {
	switch p {
	case ProviderDockerHub:
		return "docker.io"
	case ProviderGHCR:
		return "ghcr.io"
	}
	return ""
}

// Settings is the platform image registry configuration. Tokens are stored
// sealed only and are write-only for every outside reader.
type Settings struct {
	Provider            Provider
	Account, Repository string
	PushTokenCiphertext string
	PullTokenCiphertext string
	UpdatedAt           time.Time
}

func NewSettings(provider, account, repository string, now time.Time) (Settings, error) {
	p := Provider(strings.TrimSpace(provider))
	repo := strings.TrimSpace(repository)
	acct := strings.TrimSpace(account)
	// an account with ':' cannot be sent as a pull credential (the laboratory agent refuses it)
	if !p.Valid() || acct == "" || strings.Contains(acct, ":") || repo == "" || strings.ContainsAny(repo, " \t\r\n") || strings.Contains(repo, "://") {
		return Settings{}, ErrInvalidSettings.Err()
	}
	return Settings{Provider: p, Account: acct, Repository: repo, UpdatedAt: now}, nil
}

// SetTokens stores sealed tokens; an empty value keeps the stored one
// (tokens are write-only, so the client cannot send them back unchanged).
func (s *Settings) SetTokens(push, pull string, now time.Time) {
	if push != "" {
		s.PushTokenCiphertext = push
	}
	if pull != "" {
		s.PullTokenCiphertext = pull
	}
	s.UpdatedAt = now
}

func (s *Settings) Configured() bool {
	return s.Provider.Valid() && s.Repository != "" && s.PushTokenCiphertext != ""
}

// ClearPullToken removes the pull token: pulls then use the push token.
func (s *Settings) ClearPullToken(now time.Time) {
	s.PullTokenCiphertext = ""
	s.UpdatedAt = now
}

// SameAccount reports whether the settings belong to this provider account, so
// its stored tokens still apply (account names are case-insensitive).
func (s *Settings) SameAccount(provider Provider, account string) bool {
	return s.Provider == provider && strings.EqualFold(s.Account, strings.TrimSpace(account))
}
