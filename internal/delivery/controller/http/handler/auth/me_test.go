package auth

import (
	"testing"

	"github.com/cybericebox/daemon/internal/model/rbac"
)

func TestPermissionsForRole(t *testing.T) {
	contains := func(xs []string, want string) bool {
		for _, x := range xs {
			if x == want {
				return true
			}
		}
		return false
	}

	super := rbac.PermissionStrings(rbac.RoleSuperAdmin)
	if len(super) != 1 || super[0] != "*" {
		t.Fatalf("super_admin should hold [\"*\"], got %v", super)
	}

	admin := rbac.PermissionStrings(rbac.RoleAdmin)
	if !contains(admin, "users") || !contains(admin, "notifications.self") {
		t.Fatalf("admin should hold users + notifications.self, got %v", admin)
	}
	if contains(admin, "platform.settings") {
		t.Fatalf("admin must not hold platform.settings, got %v", admin)
	}

	user := rbac.PermissionStrings(rbac.RoleUser)
	if !contains(user, "notifications.self") || !contains(user, "self") {
		t.Fatalf("user should hold notifications.self + self, got %v", user)
	}
	if contains(user, "users") || contains(user, "*") {
		t.Fatalf("user must not hold admin perms, got %v", user)
	}

	if len(rbac.PermissionStrings("nonsense")) != 0 {
		t.Fatalf("unknown role should hold nothing, got %v", rbac.PermissionStrings("nonsense"))
	}
}
