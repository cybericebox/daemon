package config_test

import (
	"testing"

	"github.com/cybericebox/daemon/internal/config"
)

func TestValidateSiteLinks(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		ok          bool
	}{
		{"empty", "", true},
		{"host", "https://example.org", true},
		{"slash", "https://example.org/", true},
		{"path", "https://example.org/legal/privacy", true},
		{"query", "https://example.org/p?lang=uk", true},
		{"subdomain", "https://www.example.org/terms", true},
		{"http", "http://example.org", false},
		{"no scheme", "example.org", false},
		{"other scheme", "javascript:alert(1)", false},
		{"no host", "https://", false},
		{"opaque", "https:example.org", false},
		{"userinfo", "https://user@example.org", false},
		{"port", "https://example.org:8443/", true},
		{"bad port", "https://example.org:port/", false},
		{"fragment", "https://example.org/#top", false},
		{"empty fragment", "https://example.org/#", false},
		{"space", "https://example.org/a b", false},
		{"newline", "https://example.org/\n", false},
		{"double quote", `https://example.org/"`, false},
		{"single quote", "https://example.org/'", false},
		{"angle", "https://example.org/<", false},
		{"backslash", `https://example.org/\`, false},
		{"upper case host", "https://Example.org", false},
		{"ip literal", "https://[::1]/", false},
	} {
		for _, c := range []config.AuthConfig{{MainSiteURL: tc.value}, {TermsURL: tc.value}, {PrivacyURL: tc.value}} {
			if err := c.ValidateSiteLinks(); (err == nil) != tc.ok {
				t.Errorf("%s (%q): err=%v want ok=%v", tc.name, tc.value, err, tc.ok)
			}
		}
	}
}

func TestSiteURLFallsBackToTheIDSite(t *testing.T) {
	c := config.AuthConfig{Hosts: config.HostsConfig{ID: "id.example.test"}}
	if got := c.SiteURL(); got != "https://id.example.test" {
		t.Fatalf("not set: %q", got)
	}
	c.MainSiteURL = "https://example.org"
	if got := c.SiteURL(); got != "https://example.org" {
		t.Fatalf("set: %q", got)
	}
}
