package tools

import "testing"

func TestPathOf(t *testing.T) {
	cases := map[string]string{
		"https://id.example.test/sign-in?x=1": "/sign-in",
		"https://example.test":                "/",
		"::bad::":                             "/",
		"":                                    "/",
	}
	for in, want := range cases {
		if got := PathOf(in); got != want {
			t.Errorf("PathOf(%q)=%q want %q", in, got, want)
		}
	}
}

func TestHostFromURL(t *testing.T) {
	cases := map[string]string{
		"https://ID.Example.test/x": "id.example.test",
		"https://example.test.:443": "example.test",
		"https://a.b.example.test":  "a.b.example.test",
		"no-scheme/x":               "",
	}
	for in, want := range cases {
		if got := HostFromURL(in); got != want {
			t.Errorf("HostFromURL(%q)=%q want %q", in, got, want)
		}
	}
}

func TestSubdomainOfHost(t *testing.T) {
	apex := "example.test"
	type R struct {
		sub string
		ok  bool
	}
	cases := map[string]R{
		"example.test":     {"", true},    // apex itself
		"id.example.test":  {"id", true},  // single label
		"a.b.example.test": {"a.b", true}, // multi-label
		"evilexample.test": {"", false},   // sibling, not a subdomain
		"other.test":       {"", false},   // off-platform
	}
	for host, want := range cases {
		sub, ok := SubdomainOfHost(host, apex)
		if sub != want.sub || ok != want.ok {
			t.Errorf("SubdomainOfHost(%q)=(%q,%v) want (%q,%v)", host, sub, ok, want.sub, want.ok)
		}
	}
}
