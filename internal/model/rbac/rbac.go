package rbac

// Role is a coarse, static authorization role; each maps to a held permission
// set (see permission.go). Stored on the user as a string.
type Role string

const (
	RoleSuperAdmin  Role = "super_admin"
	RoleAdmin       Role = "admin"
	RoleAdminViewer Role = "admin_viewer"
	RoleUser        Role = "user"
	RolePublic      Role = "public" // unauthenticated sentinel; never stored
)

// ValidRole reports whether s is an assignable, stored role (excludes public).
func ValidRole(s string) bool {
	switch Role(s) {
	case RoleSuperAdmin, RoleAdmin, RoleAdminViewer, RoleUser:
		return true
	default:
		return false
	}
}

// IsAdminTier reports whether the role is permitted to access the admin
// subdomain. super_admin, admin, and admin_viewer all pass the coarse gate;
// user, public, and unknown roles do not.
func (r Role) IsAdminTier() bool {
	switch r {
	case RoleSuperAdmin, RoleAdmin, RoleAdminViewer:
		return true
	default:
		return false
	}
}

func (r Role) Permissions() []Permission {
	return rolePermissions[r]
}

func (r Role) HasPermission(required Permission) bool {
	for _, held := range r.Permissions() {
		if covers(held, required) {
			return true
		}
	}
	return false
}

func RolesFromStrings(rolesStrings []string) []Role {
	roles := make([]Role, len(rolesStrings))
	for i, roleString := range rolesStrings {
		roles[i] = Role(roleString)
	}
	return roles
}
