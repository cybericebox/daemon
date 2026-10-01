package auth_test

import (
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/useCase/auth"
)

func newUCWithDomain(t *testing.T, domain string) *auth.AuthUseCase {
	t.Helper()
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Config: config.AuthConfig{
			Domain:         domain,
			SessionIdleTTL: time.Hour,
		},
	})
	return uc
}

func TestIsTrustedRedirect_Subdomain_True(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	if !uc.IsTrustedRedirect("https://app.example.com/cb") {
		t.Fatal("want true for subdomain of platform domain")
	}
}

func TestIsTrustedRedirect_ExactDomain_True(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	if !uc.IsTrustedRedirect("https://example.com/cb") {
		t.Fatal("want true for exact platform domain")
	}
}

func TestIsTrustedRedirect_EvilDomain_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	if uc.IsTrustedRedirect("https://evil.com/cb") {
		t.Fatal("want false for untrusted domain")
	}
}

func TestIsTrustedRedirect_EmptyHost_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	if uc.IsTrustedRedirect("/relative/path") {
		t.Fatal("want false for relative URL (no host)")
	}
}

func TestIsTrustedRedirect_SubdomainLookAlike_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// evilexample.com should NOT match
	if uc.IsTrustedRedirect("https://evilexample.com/cb") {
		t.Fatal("want false for domain that merely ends with example.com but is not a subdomain")
	}
}

func TestIsTrustedRedirect_JavascriptScheme_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// javascript: scheme must be rejected even when the host matches
	if uc.IsTrustedRedirect("javascript://app.example.com/x") {
		t.Fatal("want false for javascript:// scheme (non-http bypass)")
	}
}

func TestIsTrustedRedirect_UserinfoPresent_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// userinfo in URL must be rejected (https://attacker@platform.example.com/…)
	if uc.IsTrustedRedirect("https://attacker@app.example.com/x") {
		t.Fatal("want false for URL with userinfo (credential bypass)")
	}
}

func TestIsTrustedRedirect_UppercaseHost_True(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// host header may arrive uppercased — must still be trusted
	if !uc.IsTrustedRedirect("https://APP.EXAMPLE.COM/cb") {
		t.Fatal("want true for trusted subdomain with uppercased host")
	}
}

// --- new contract tests: https-only, no port ---

func TestIsTrustedRedirect_HTTP_SameDomain_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// http (non-TLS) must be rejected even for the exact platform domain
	if uc.IsTrustedRedirect("http://example.com/cb") {
		t.Fatal("want false for http scheme (https only)")
	}
}

func TestIsTrustedRedirect_HTTP_Subdomain_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// http subdomain must also be rejected
	if uc.IsTrustedRedirect("http://app.example.com/cb") {
		t.Fatal("want false for http subdomain (https only)")
	}
}

func TestIsTrustedRedirect_HTTPS_WithPort_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// explicit port must be rejected even on a trusted domain
	if uc.IsTrustedRedirect("https://id.example.com:3001/x") {
		t.Fatal("want false for https URL with explicit port")
	}
}

func TestIsTrustedRedirect_HTTPS_Apex_True(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	// bare (apex) domain over https, no port — must be trusted
	if !uc.IsTrustedRedirect("https://example.com/login") {
		t.Fatal("want true for https apex domain")
	}
}

func TestIsTrustedRedirect_Garbage_False(t *testing.T) {
	uc := newUCWithDomain(t, "example.com")
	for _, bad := range []string{"", "not-a-url", "://broken", "ftp://example.com/x"} {
		if uc.IsTrustedRedirect(bad) {
			t.Fatalf("want false for garbage/invalid input %q", bad)
		}
	}
}
