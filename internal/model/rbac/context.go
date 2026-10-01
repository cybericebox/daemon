package rbac

import (
	"context"

	"github.com/gofrs/uuid"
)

// Claims is the authenticated identity resolved from a validated session
// cookie — the one bundle that travels through a request's context. It lives
// in rbac (not model/auth) so this package does not import model/auth, which
// imports rbac: that would be an import cycle. model/auth re-exports it as
// AuthClaims via a type alias.
type Claims struct {
	SessionID uuid.UUID
	UserID    uuid.UUID
	Role      Role
}

type currentUserSessionCtxKey struct{}

// HasPermissionInContext reports whether the caller (role from ctx) holds a
// permission covering required. The single seam handlers/use-cases check.
func HasPermissionInContext(ctx context.Context, required Permission) bool {
	claims, ok := CurrentUserSessionFromContext(ctx)
	if !ok {
		return false
	}
	return claims.Role.HasPermission(required)
}

// ContextWithCurrentUserSession stores the caller's identity in ctx. Identity
// is always written and read as one bundle — there are deliberately no
// per-field producers or getters.
func ContextWithCurrentUserSession(ctx context.Context, claims Claims) context.Context {
	return context.WithValue(ctx, currentUserSessionCtxKey{}, claims)
}

// CurrentUserSessionFromContext returns the caller's identity and whether an
// authenticated session is present (fail-closed — callers reject with 401).
func CurrentUserSessionFromContext(ctx context.Context) (Claims, bool) {
	if claims, ok := ctx.Value(currentUserSessionCtxKey{}).(Claims); ok {
		return claims, true
	}
	return Claims{}, false
}

func MustCurrentUserSessionFromContext(ctx context.Context) Claims {
	claims, ok := CurrentUserSessionFromContext(ctx)
	if !ok {
		panic("no current user session in context")
	}
	return claims
}
