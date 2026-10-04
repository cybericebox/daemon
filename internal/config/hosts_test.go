package config

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func validHosts() HostsConfig {
	return HostsConfig{
		Main: "cybericebox.com", API: "api.cybericebox.com", ID: "id.cybericebox.com",
		Admin: "admin.cybericebox.com", Exercises: "exercises.cybericebox.com", EventDomain: "cybericebox.com",
	}
}

func TestHostsValidate(t *testing.T) {
	h := HostsConfig{Domain: "cybericebox.com"}
	if err := h.Validate(); err != nil || h != validHosts2() {
		t.Fatalf("every host derives from the domain: err %v, got %+v", err, h)
	}
	for name, domain := range map[string]string{
		"empty": "", "scheme": "https://cybericebox.com", "port": "cybericebox.com:443", "upper": "CyberIceBox.com", "suffix": "com",
	} {
		h := HostsConfig{Domain: domain}
		if err := h.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	h = HostsConfig{Domain: "a.pp.ua"}
	if err := h.Validate(); err != nil || h.API != "api.a.pp.ua" {
		t.Fatalf("a multi-label public suffix is handled: %v %+v", err, h)
	}
}

func validHosts2() HostsConfig {
	h := validHosts()
	h.Domain = "cybericebox.com"
	return h
}

func TestHostsLinksAndOrigins(t *testing.T) {
	h := validHosts()
	if got := h.IDURL("/setup?token=t"); got != "https://id.cybericebox.com/setup?token=t" {
		t.Fatalf("IDURL: %s", got)
	}
	if got := h.APIURL("/api/auth/google/callback"); got != "https://api.cybericebox.com/api/auth/google/callback" {
		t.Fatalf("APIURL: %s", got)
	}
	if got := h.EventURL("spring"); got != "https://spring.cybericebox.com/" {
		t.Fatalf("EventURL: %s", got)
	}
	reserved := h.ReservedEventTags()
	for _, tag := range []string{"api", "id", "admin", "exercises"} {
		if !reserved[tag] {
			t.Errorf("%s must be reserved", tag)
		}
	}
	for host, want := range map[string]bool{
		"cybericebox.com": true, "id.cybericebox.com": true, "admin.cybericebox.com": true, "exercises.cybericebox.com": true,
		"spring.cybericebox.com": true, "SPRING.cybericebox.com": true,
		"api.cybericebox.com": false, "a.b.cybericebox.com": false, "evilcybericebox.com": false, "cybericebox.com.evil.test": false,
	} {
		if got := h.IsFrontendOrigin(host); got != want {
			t.Errorf("IsFrontendOrigin(%s) = %v, want %v", host, got, want)
		}
	}
	if tag, ok := h.EventTag("spring.cybericebox.com"); !ok || tag != "spring" {
		t.Fatalf("EventTag: %q %v", tag, ok)
	}
	for _, host := range []string{"cybericebox.com", "id.cybericebox.com", "api.cybericebox.com", "x.y.cybericebox.com", "spring.example.org"} {
		if _, ok := h.EventTag(host); ok {
			t.Errorf("%s is not an event site", host)
		}
	}
}

func TestHostsOutsideEventDomain(t *testing.T) {
	h := HostsConfig{Main: "x.test", API: "api.x.test", ID: "id.x.test", Admin: "admin.x.test", Exercises: "ex.x.test", EventDomain: "events.x.test"}
	if !h.ReservedEventTags()["api"] {
		t.Fatalf("the fixed tags stay reserved: %v", h.ReservedEventTags())
	}
	if h.IsFrontendOrigin("events.x.test") || !h.IsFrontendOrigin("a.events.x.test") || !strings.HasSuffix(h.EventURL("a"), "a.events.x.test/") {
		t.Fatal("the event domain apex is not a frontend; its labels are event sites")
	}
}

func TestOriginAllowed(t *testing.T) {
	h := HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test",
		Exercises: "exercises.example.test", EventDomain: "events.example.test"}
	for origin, want := range map[string]bool{
		"https://example.test":             true,
		"https://id.example.test":          true,
		"https://admin.example.test":       true,
		"https://exercises.example.test":   true,
		"https://api.example.test":         true, // same-origin calls
		"https://ctf.events.example.test":  true, // one label under EVENT_DOMAIN
		"https://CTF.Events.Example.Test":  true,
		"https://web-x.labs.example.test":  false, // a lab device page
		"https://a.b.events.example.test":  false, // two labels under EVENT_DOMAIN
		"https://events.example.test":      false, // the event domain itself
		"https://evil-example.test":        false, // lookalikes
		"https://id.example.test.evil.com": false,
		"https://example.test.evil.com":    false,
		"https://xid.example.test":         false,
		"http://id.example.test":           false, // not https
		"http://ctf.events.example.test":   false,
		"https://user@id.example.test":     false,
		"null":                             false,
		"":                                 false,
		"file:///etc/passwd":               false,
		"data:text/html,x":                 false,
		"https://id.example.test:8443":     true, // the port is not part of the host check
	} {
		if _, got := h.OriginAllowed(origin); got != want {
			t.Errorf("OriginAllowed(%q)=%v want %v", origin, got, want)
		}
	}
}

// TestResolveHostsVectors runs the shared base-domain vectors (the same file lives in every repository).
func TestResolveHostsVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/base-domain-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Cases []struct {
			Name          string            `json:"name"`
			Domain        string            `json:"domain"`
			Expect        map[string]string `json:"expect"`
			ErrorContains []string          `json:"error_contains"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors.Cases) == 0 {
		t.Fatal("no cases")
	}
	for _, c := range vectors.Cases {
		t.Run(c.Name, func(t *testing.T) {
			got, err := ResolveHosts(c.Domain)
			if c.ErrorContains != nil {
				if err == nil {
					t.Fatalf("want an error, got %v", got)
				}
				for _, s := range c.ErrorContains {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q lacks %q", err, s)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, c.Expect) {
				t.Fatalf("got %v, want %v", got, c.Expect)
			}
		})
	}
}

// The fixed subdomains stay refused as event tags, whatever the domain is.
func TestReservedEventTagsAreFixed(t *testing.T) {
	h := HostsConfig{Domain: "example.test"}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"api", "id", "admin", "exercises", "labs", "vpn", "ctl", "www"} {
		if !h.ReservedEventTags()[tag] {
			t.Errorf("%s must be reserved", tag)
		}
		if _, ok := h.EventTag(tag + ".example.test"); ok {
			t.Errorf("%s.example.test must not be an event site", tag)
		}
		if h.IsFrontendOrigin(tag+".example.test") && tag != "id" && tag != "admin" && tag != "exercises" {
			t.Errorf("%s.example.test must not be a frontend origin", tag)
		}
	}
	if tag, ok := h.EventTag("spring.example.test"); !ok || tag != "spring" {
		t.Fatalf("EventTag: %q %v", tag, ok)
	}
}
