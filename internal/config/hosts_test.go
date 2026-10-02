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

func TestLabsDomain(t *testing.T) {
	h := HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test",
		Exercises: "exercises.example.test", EventDomain: "example.test", LabsDomain: " Labs.Example.Test. "}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	if h.LabsDomain != "labs.example.test" {
		t.Fatalf("normalized: %q", h.LabsDomain)
	}
	for host, want := range map[string]bool{"labs.example.test": true, "web-1.labs.example.test": true, "WEB.LABS.EXAMPLE.TEST.": true, "evillabs.example.test": false, "example.test": false, "labs.example.test.evil.com": false} {
		if got := h.IsLabsHost(host); got != want {
			t.Errorf("IsLabsHost(%q)=%v want %v", host, got, want)
		}
	}
	if (HostsConfig{}).IsLabsHost("labs.example.test") {
		t.Error("no labs domain configured: nothing is a labs host")
	}
	h.LabsDomain = "https://labs.example.test"
	if h.Validate() == nil {
		t.Error("a scheme in LABS_DOMAIN must be refused")
	}
}
