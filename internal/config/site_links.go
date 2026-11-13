// Package config reads and validates the daemon configuration from the environment.
package config

import (
	"fmt"
	"net/url"
	"strings"
)

// SiteURL is the site address mail footers use as {site_url}: MAIN_SITE_URL, or the ID site when it is not set.
func (c AuthConfig) SiteURL() string {
	if c.MainSiteURL != "" {
		return c.MainSiteURL
	}
	return c.Hosts.IDURL("")
}

// RequiredDocuments are the documents a new account must accept: "terms" when TERMS_URL is set and "privacy" when
// PRIVACY_URL is set. Empty when the installation has neither, then nothing is required and nothing is recorded.
func (c AuthConfig) RequiredDocuments() []string {
	var required []string
	if c.TermsURL != "" {
		required = append(required, "terms")
	}
	if c.PrivacyURL != "" {
		required = append(required, "privacy")
	}
	return required
}

// ValidateSiteLinks checks the optional MAIN_SITE_URL, TERMS_URL and PRIVACY_URL. Each is empty or an https URL.
func (c AuthConfig) ValidateSiteLinks() error {
	for _, link := range []struct{ name, value string }{
		{"MAIN_SITE_URL", c.MainSiteURL}, {"TERMS_URL", c.TermsURL}, {"PRIVACY_URL", c.PrivacyURL},
	} {
		if err := validateOptionalURL(link.name, link.value); err != nil {
			return err
		}
	}
	return nil
}

// validateOptionalURL accepts an empty value or https://host[/path][?query]: a lower case host name with an optional port,
// no userinfo, no fragment, no whitespace or control characters and none of " ' < > \. The same rule keeps the value
// safe inside an HTML attribute where a frontend substitutes it.
const schemeHTTPS = "https"

func validateOptionalURL(name, value string) error {
	if value == "" {
		return nil
	}
	if strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r == 0x7f || strings.ContainsRune(`"'<>\`, r) }) {
		return fmt.Errorf("%s must not contain whitespace, control characters or any of \" ' < > \\", name)
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL", name)
	}
	switch {
	case parsed.Scheme != schemeHTTPS:
		return fmt.Errorf("%s must start with https://", name)
	case parsed.Host == "" || parsed.Opaque != "":
		return fmt.Errorf("%s must name a host", name)
	case parsed.User != nil:
		return fmt.Errorf("%s must not contain credentials", name)
	case strings.Contains(value, "#"):
		return fmt.Errorf("%s must not contain a fragment", name)
	case len(parsed.Hostname()) > 253 || !domainPattern.MatchString(parsed.Hostname()):
		return fmt.Errorf("%s must name a lower case host", name)
	}
	return nil
}
