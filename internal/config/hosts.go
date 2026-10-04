package config

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// HostsConfig names every public host of the platform. DOMAIN is the only input: every host derives
// from it (see ResolveHosts) and there are no per-host settings, so all hosts always share one
// registrable domain and the host-only session cookie of the API stays same-site for every frontend
// (SameSite=Strict).
type HostsConfig struct {
	// Domain is the base domain (env DOMAIN, required).
	Domain string `env:"DOMAIN,required"`
	// Main is the landing host (mail footer links): DOMAIN.
	Main string `env:"-"`
	// API is the only Host this service answers on; the OAuth redirect URI and the public API base URL
	// are built from it: api.DOMAIN.
	API string `env:"-"`
	// ID is the sign-in app host: sign-in, setup and confirmation links point at it: id.DOMAIN.
	ID string `env:"-"`
	// Admin and Exercises are the admin and the exercise catalog app hosts: admin.DOMAIN, exercises.DOMAIN.
	Admin     string `env:"-"`
	Exercises string `env:"-"`
	// EventDomain holds the event sites: <tag>.<EventDomain>; it is DOMAIN.
	EventDomain string `env:"-"`
}

// hostDerivations is the base-domain rule: each host key with the prefix put before DOMAIN.
// COOKIE_DOMAIN is for the frontends; the daemon does not use it but resolves it so the rule is the
// same everywhere. Every implementation runs testdata/base-domain-vectors.json (identical copies in
// every repository).
var hostDerivations = []struct{ Key, Prefix string }{
	{"MAIN_HOST", ""}, {"API_HOST", "api."}, {"ID_HOST", "id."}, {"ADMIN_HOST", "admin."},
	{"EXERCISES_HOST", "exercises."}, {"EVENT_DOMAIN", ""}, {"COOKIE_DOMAIN", ""},
}

var domainPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// ResolveHosts applies the base-domain rule: domain must be set and be a bare lower case host name,
// and every host is <prefix>DOMAIN.
func ResolveHosts(domain string) (map[string]string, error) {
	if domain == "" {
		return nil, fmt.Errorf("DOMAIN is required")
	}
	if len(domain) > 253 || !domainPattern.MatchString(domain) {
		return nil, fmt.Errorf("DOMAIN must be a bare lower case host name (no scheme, port or path), got %q", domain)
	}
	out := make(map[string]string, len(hostDerivations))
	for _, d := range hostDerivations {
		out[d.Key] = d.Prefix + domain
	}
	return out, nil
}

// Validate derives every host from Domain and checks that Domain sits under a registrable domain.
func (h *HostsConfig) Validate() error {
	got, err := ResolveHosts(h.Domain)
	if err != nil {
		return err
	}
	if _, err := publicsuffix.EffectiveTLDPlusOne(h.Domain); err != nil {
		return fmt.Errorf("DOMAIN must sit under a registrable domain: %q", h.Domain)
	}
	h.Main, h.API, h.ID, h.Admin, h.Exercises, h.EventDomain = got["MAIN_HOST"], got["API_HOST"], got["ID_HOST"], got["ADMIN_HOST"], got["EXERCISES_HOST"], got["EVENT_DOMAIN"]
	return nil
}

// OriginAllowed is THE allow-list of browser origins, shared by CORS and the origin guard so they
// cannot drift apart: https only, and the host is exactly MAIN, ID, ADMIN, EXERCISES, API (for
// same-origin calls) or an event site, which is exactly one label under EVENT_DOMAIN. Anything else
// (a lab device page web-x.labs.<domain>, a two-label host under EVENT_DOMAIN, a lookalike domain,
// "null", http) is refused. On refusal reason is a short text for logs, never for the client.
func (h HostsConfig) OriginAllowed(origin string) (reason string, ok bool) {
	parsed, err := url.Parse(origin)
	if err != nil {
		return "unparseable origin", false
	}
	if parsed.Scheme != "https" {
		return "scheme is not https (got " + parsed.Scheme + ")", false
	}
	if parsed.Host == "" || parsed.User != nil {
		return "empty host or credentials in the origin", false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	if host == h.API || h.IsFrontendOrigin(host) {
		return "", true
	}
	return "host is neither a platform host nor an event site", false
}

// URL returns https://<host><path>.
func URL(host, path string) string { return "https://" + host + path }

// IDURL is a link into the sign-in app, path starting with "/".
func (h HostsConfig) IDURL(path string) string { return URL(h.ID, path) }

// APIURL is the public API base URL when path is empty.
func (h HostsConfig) APIURL(path string) string { return URL(h.API, path) }

// EventURL is the home page of the event site with tag.
func (h HostsConfig) EventURL(tag string) string { return URL(tag+"."+h.EventDomain, "/") }

// reservedEventTags are the labels of the fixed platform subdomains (the app hosts and the laboratory
// names): an event can never take one of them as its tag, so <tag>.DOMAIN never collides with them.
var reservedEventTags = map[string]bool{
	"api": true, "id": true, "admin": true, "exercises": true, "labs": true, "vpn": true, "ctl": true, "www": true,
}

// ReservedEventTags returns the reserved event tags (a copy).
func (h HostsConfig) ReservedEventTags() map[string]bool {
	tags := make(map[string]bool, len(reservedEventTags))
	for t := range reservedEventTags {
		tags[t] = true
	}
	return tags
}

// IsPlatformHost reports whether host is one of the platform hosts (the five named hosts, or an
// event site under EventDomain).
func (h HostsConfig) IsPlatformHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case h.Main, h.API, h.ID, h.Admin, h.Exercises:
		return true
	}
	return h.isEventSite(host)
}

// IsFrontendOrigin is the CORS and redirect allowlist: exactly the frontend hosts and the main host,
// plus any event site <label>.<EventDomain>. The API host itself is not a frontend.
func (h HostsConfig) IsFrontendOrigin(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case h.Main, h.ID, h.Admin, h.Exercises:
		return true
	}
	return h.isEventSite(host)
}

func (h HostsConfig) isEventSite(host string) bool {
	label, ok := strings.CutSuffix(host, "."+h.EventDomain)
	return ok && label != "" && !strings.Contains(label, ".") && !reservedEventTags[label]
}

// EventTag returns the event tag of an event site host.
func (h HostsConfig) EventTag(host string) (string, bool) {
	host = strings.ToLower(host)
	label, ok := strings.CutSuffix(host, "."+h.EventDomain)
	if !ok || label == "" || strings.Contains(label, ".") || reservedEventTags[label] {
		return "", false
	}
	return label, true
}
