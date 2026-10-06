package tools

import (
	"net/url"
	"strings"
)

// PathOf returns rawURL's path component, or "/" when unparseable or empty.
func PathOf(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Path == "" {
		return "/"
	}
	return parsed.Path
}

// HostFromURL returns the lowercased host of a scheme://host/... URL with any
// trailing dot and port stripped. Returns "" when rawURL has no scheme.
func HostFromURL(rawURL string) string {
	idx := strings.Index(rawURL, "://")
	if idx < 0 {
		return ""
	}
	host := strings.ToLower(rawURL[idx+3:])
	if i := strings.Index(host, "/"); i >= 0 {
		host = host[:i]
	}
	// Strip port first, then trailing dot (handles "example.test.:443").
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	host = strings.TrimSuffix(host, ".")
	return host
}

// SubdomainOfHost returns the subdomain label(s) of host under apex. ok is false
// when host is neither the apex nor under it (off-platform). The apex itself
// returns ("", true); a host under the apex returns (label(s), true). Anchored on
// the dot before the apex so a sibling like "evilexample.test" is NOT read as a
// subdomain of "example.test".
func SubdomainOfHost(host, apex string) (string, bool) {
	if host == apex {
		return "", true
	}
	suffix := "." + apex
	if !strings.HasSuffix(host, suffix) {
		return "", false
	}
	return strings.TrimSuffix(host, suffix), true
}
