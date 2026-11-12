package rbac

type permissionSet struct {
	permissions []Permission
	inherits    []Role
}

func newRolePerms(permissions ...Permission) permissionSet {
	return permissionSet{
		permissions: permissions,
	}
}

// InheritRoles records the parent roles whose permission sets this one
// includes. Resolution is deferred to init() in permission.go: at the time the
// rolePermissionsSet map literal is evaluated, rolePermissions is still empty,
// so reading role.Permissions() here would silently inherit nothing.
func (p permissionSet) InheritRoles(roles ...Role) permissionSet {
	p.inherits = append(p.inherits, roles...)
	return p
}
