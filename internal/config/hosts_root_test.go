package config

import "testing"

// The root domain (DOMAIN itself) keeps every role it has: allowed origin, frontend origin, platform host.
// The table is written without the field that holds the root host, so it holds before and after any rename.
func TestRootDomainKeepsItsRoles(t *testing.T) {
	h := HostsConfig{Domain: "example.test"}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	for origin, want := range map[string]bool{
		"https://example.test":             true, // the root domain (apex)
		"https://EXAMPLE.test":             true,
		"https://example.test.":            true,
		"https://example.test:8443":        true,
		"https://id.example.test":          true,
		"https://admin.example.test":       true,
		"https://exercises.example.test":   true,
		"https://api.example.test":         true,
		"https://ctf.example.test":         true, // an event site
		"https://a.b.example.test":         false,
		"https://evil-example.test":        false, // lookalike
		"https://example.test.evil.com":    false,
		"https://xexample.test":            false,
		"http://example.test":              false,
		"null":                             false,
		"https://user@example.test":        false,
		"https://id.example.test.evil.com": false,
	} {
		if _, got := h.OriginAllowed(origin); got != want {
			t.Errorf("OriginAllowed(%q)=%v want %v", origin, got, want)
		}
	}
	if !h.IsPlatformHost("example.test") || !h.IsFrontendOrigin("example.test") {
		t.Error("the root domain is a platform host and a frontend origin")
	}
	if !h.IsPlatformHost("api.example.test") || h.IsFrontendOrigin("api.example.test") {
		t.Error("the API host is a platform host but not a frontend origin")
	}
	if h.IsPlatformHost("evil-example.test") || h.IsFrontendOrigin("evil-example.test") {
		t.Error("a lookalike is neither")
	}
}
