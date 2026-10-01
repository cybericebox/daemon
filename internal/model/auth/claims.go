package authModel

import (
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// AuthClaims are resolved from a validated session cookie. There is exactly one
// credential now (the session cookie, host-only on api.<domain>) — it is not
// scoped to a calling frontend; which frontend may do what is enforced by RBAC
// permissions and the CORS/Origin allowlist, not by which credential is present.
// Alias of rbac.Claims: the struct lives in rbac so the context helpers there
// can accept it without importing this package (import cycle).
type AuthClaims = rbac.Claims

// SessionCookie is the single platform credential: a __Host- prefixed cookie
// (browser-enforced Secure + Path=/ + no Domain attribute — i.e. host-only),
// set on api.<domain> only, SameSite=Strict.
const SessionCookie = "__Host-session"
