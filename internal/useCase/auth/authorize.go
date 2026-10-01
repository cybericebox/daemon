package auth

import (
	"net/url"
	"strings"
)

// IsTrustedRedirect reports whether target is safe to redirect to after auth.
// Contract: https only, no port, no userinfo, same-platform-domain (exact or
// subdomain). Rejects http, explicit ports, userinfo credentials, and any
// off-platform host to prevent open-redirect and credential-bypass attacks.
func (u *AuthUseCase) IsTrustedRedirect(target string) bool {
	parsed, err := url.Parse(target)
	if err != nil || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "https" {
		return false
	}
	if parsed.Port() != "" {
		return false
	}
	if parsed.User != nil {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	dom := strings.ToLower(u.cfg.Domain)
	return host == dom || strings.HasSuffix(host, "."+dom)
}
