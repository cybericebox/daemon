package config

import (
	"fmt"
	"net/url"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// HostsConfig names every public host of the platform. All of them are required: a missing one stops
// the start. They have to share one registrable domain, so the host-only session cookie of the API
// stays same-site for every frontend (SameSite=Strict).
type HostsConfig struct {
	// Main is the landing host (mail footer links).
	Main string `env:"MAIN_HOST,required"`
	// API is the only Host this service answers on; the OAuth redirect URI and the public API base URL
	// are built from it.
	API string `env:"API_HOST,required"`
	// ID is the sign-in app host: sign-in, setup and confirmation links point at it.
	ID string `env:"ID_HOST,required"`
	// Admin and Exercises are the admin and the exercise catalog app hosts.
	Admin     string `env:"ADMIN_HOST,required"`
	Exercises string `env:"EXERCISES_HOST,required"`
	// EventDomain holds the event sites: <tag>.<EventDomain>.
	EventDomain string `env:"EVENT_DOMAIN,required"`
	// LabsDomain is the base domain of the lab device pages (<device>-<code>.<LABS_DOMAIN>). It is
	// optional here (no laboratory, no value), but when set the API refuses every state-changing
	// request that comes from a page under it: those pages run task-controlled content on a domain
	// that is same-site with the platform, so SameSite=Strict does not keep them out.
	LabsDomain string `env:"LABS_DOMAIN"`
}

// Validate normalises the hosts (lower case) and checks that each is a bare host name under one
// registrable domain.
func (h *HostsConfig) Validate() error {
	fields := []struct {
		env string
		val *string
	}{
		{"MAIN_HOST", &h.Main}, {"API_HOST", &h.API}, {"ID_HOST", &h.ID},
		{"ADMIN_HOST", &h.Admin}, {"EXERCISES_HOST", &h.Exercises}, {"EVENT_DOMAIN", &h.EventDomain},
	}
	registrable := ""
	for _, f := range fields {
		v := strings.ToLower(strings.TrimSpace(*f.val))
		if v == "" || strings.ContainsAny(v, "/:@?# ") || strings.HasPrefix(v, ".") || strings.HasSuffix(v, ".") {
			return fmt.Errorf("%s must be a bare host name (no scheme, port or path), got %q", f.env, *f.val)
		}
		if u, err := url.Parse("https://" + v); err != nil || u.Hostname() != v {
			return fmt.Errorf("%s is not a valid host name: %q", f.env, *f.val)
		}
		reg, err := publicsuffix.EffectiveTLDPlusOne(v)
		if err != nil {
			return fmt.Errorf("%s must sit under a registrable domain: %q", f.env, v)
		}
		if registrable == "" {
			registrable = reg
		} else if reg != registrable {
			return fmt.Errorf("%s (%s) must share the registrable domain %s with the other hosts", f.env, v, registrable)
		}
		*f.val = v
	}
	if labs := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h.LabsDomain), ".")); labs != "" {
		if strings.ContainsAny(labs, "/:@?# ") || strings.HasPrefix(labs, ".") {
			return fmt.Errorf("LABS_DOMAIN must be a bare domain name (no scheme, port or path), got %q", h.LabsDomain)
		}
		h.LabsDomain = labs
	}
	return nil
}

// IsLabsHost reports whether host is the labs domain or any subdomain of it.
func (h HostsConfig) IsLabsHost(host string) bool {
	if h.LabsDomain == "" {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	return host == h.LabsDomain || strings.HasSuffix(host, "."+h.LabsDomain)
}

// URL returns https://<host><path>.
func URL(host, path string) string { return "https://" + host + path }

// IDURL is a link into the sign-in app, path starting with "/".
func (h HostsConfig) IDURL(path string) string { return URL(h.ID, path) }

// APIURL is the public API base URL when path is empty.
func (h HostsConfig) APIURL(path string) string { return URL(h.API, path) }

// EventURL is the home page of the event site with tag.
func (h HostsConfig) EventURL(tag string) string { return URL(tag+"."+h.EventDomain, "/") }

// ReservedEventTags are the first labels of the platform hosts that sit under EventDomain: an event
// can never take one of them as its tag.
func (h HostsConfig) ReservedEventTags() map[string]bool {
	tags := map[string]bool{}
	for _, host := range []string{h.Main, h.API, h.ID, h.Admin, h.Exercises} {
		if label, ok := strings.CutSuffix(host, "."+h.EventDomain); ok && label != "" {
			tags[strings.SplitN(label, ".", 2)[0]] = true
		}
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
	return ok && label != "" && !strings.Contains(label, ".") && !h.ReservedEventTags()[label]
}

// EventTag returns the event tag of an event site host.
func (h HostsConfig) EventTag(host string) (string, bool) {
	host = strings.ToLower(host)
	label, ok := strings.CutSuffix(host, "."+h.EventDomain)
	if !ok || label == "" || strings.Contains(label, ".") || h.ReservedEventTags()[label] {
		return "", false
	}
	return label, true
}
