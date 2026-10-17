package imageRegistryModel

import (
	"testing"
	"time"
)

func TestProvider(t *testing.T) {
	cases := []struct {
		p                Provider
		valid            bool
		regHost, refHost string
	}{
		{ProviderDockerHub, true, "index.docker.io", "docker.io"},
		{ProviderGHCR, true, "ghcr.io", "ghcr.io"},
		{Provider("quay"), false, "", ""},
		{Provider(""), false, "", ""},
	}
	for _, c := range cases {
		if c.p.Valid() != c.valid || c.p.RegistryHost() != c.regHost || c.p.RefHost() != c.refHost {
			t.Errorf("%q: %v %q %q", c.p, c.p.Valid(), c.p.RegistryHost(), c.p.RefHost())
		}
	}
}

func TestNewSettings(t *testing.T) {
	cases := []struct {
		name                        string
		provider, account, repo     string
		wantErr                     bool
		wantAccount, wantRepository string
	}{
		{"hub", "dockerhub", " acme ", " acme/platform ", false, "acme", "acme/platform"},
		{"ghcr", "ghcr", "acme", "acme/platform", false, "acme", "acme/platform"},
		{"unknown provider", "quay", "acme", "acme/p", true, "", ""},
		{"empty repository", "ghcr", "acme", "  ", true, "", ""},
		{"scheme in repository", "ghcr", "acme", "https://ghcr.io/a", true, "", ""},
		{"space in repository", "ghcr", "acme", "a b", true, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewSettings(c.provider, c.account, c.repo, t0)
			if c.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.Account != c.wantAccount || s.Repository != c.wantRepository || !s.UpdatedAt.Equal(t0) {
				t.Fatalf("got %+v", s)
			}
		})
	}
}

func TestSettingsSetTokensAndConfigured(t *testing.T) {
	s, _ := NewSettings("ghcr", "acme", "acme/p", t0)
	if s.Configured() {
		t.Fatal("no push token yet")
	}
	later := t0.Add(time.Hour)
	s.SetTokens("push-ct", "pull-ct", later)
	if !s.Configured() || s.PushTokenCiphertext != "push-ct" || s.PullTokenCiphertext != "pull-ct" || !s.UpdatedAt.Equal(later) {
		t.Fatalf("got %+v", s)
	}
	noProvider := Settings{Repository: "a/b", PushTokenCiphertext: "x"}
	if noProvider.Configured() {
		t.Fatal("provider is required")
	}
	noRepo := Settings{Provider: ProviderGHCR, PushTokenCiphertext: "x"}
	if noRepo.Configured() {
		t.Fatal("repository is required")
	}
}
