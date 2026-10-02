package config

import (
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
	h := validHosts()
	h.Main = " CyberIceBox.com "
	if err := h.Validate(); err != nil || h.Main != "cybericebox.com" {
		t.Fatalf("a valid set is normalised: err %v main %q", err, h.Main)
	}
	for name, mutate := range map[string]func(*HostsConfig){
		"scheme":    func(h *HostsConfig) { h.API = "https://api.cybericebox.com" },
		"port":      func(h *HostsConfig) { h.ID = "id.cybericebox.com:443" },
		"empty":     func(h *HostsConfig) { h.Admin = "" },
		"other dom": func(h *HostsConfig) { h.Exercises = "exercises.example.org" },
		"suffix":    func(h *HostsConfig) { h.EventDomain = "com" },
	} {
		h := validHosts()
		mutate(&h)
		if err := h.Validate(); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	h = validHosts()
	h.Main, h.API, h.ID, h.Admin, h.Exercises, h.EventDomain = "a.pp.ua", "api.a.pp.ua", "id.a.pp.ua", "admin.a.pp.ua", "ex.a.pp.ua", "events.a.pp.ua"
	if err := h.Validate(); err != nil {
		t.Fatalf("a multi-label public suffix is handled: %v", err)
	}
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
	if len(h.ReservedEventTags()) != 0 {
		t.Fatalf("no platform host sits under the event domain: %v", h.ReservedEventTags())
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
