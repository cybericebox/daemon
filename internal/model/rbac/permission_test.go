package rbac

import "testing"

func TestHasPermission(t *testing.T) {
	cases := []struct {
		role     Role
		required Permission
		want     bool
	}{
		{RoleSuperAdmin, "users.delete", true}, // "*" covers all
		{RoleSuperAdmin, "anything.at.all", true},
		{RoleAdmin, "users.delete", true}, // "users" covers "users.delete"
		{RoleAdmin, "users.role.write", true},
		{RoleAdmin, "platform.settings.write", false}, // admin loses platform settings
		{RoleSuperAdmin, "infrastructure.read", true},
		{RoleAdmin, "infrastructure.read", false},
		{RoleAdminViewer, "infrastructure.read", false},
		{RoleSuperAdmin, "infrastructure.write", true},
		{RoleAdmin, "infrastructure.write", false},
		{RoleAdminViewer, "infrastructure.write", false},
		{RoleAdmin, "billing.read", false},                 // not held
		{RoleAdminViewer, "users.read", true},              // exact
		{RoleAdminViewer, "users.delete", false},           // read does not cover delete
		{RoleAdminViewer, "platform.settings.read", false}, // viewer loses platform settings
		{RoleAdminViewer, "platform.settings.write", false},
		{RoleAdmin, PermEventsRead, true},
		{RoleAdmin, PermEventsWrite, true},
		{RoleAdminViewer, PermEventsRead, true},
		{RoleAdminViewer, PermEventsWrite, false},
		{RoleUser, PermEventsRead, false},
		{RoleUser, PermEventsWrite, false},
		{RoleAdmin, PermExercisesRead, true},
		{RoleAdmin, PermExercisesWrite, true},
		{RoleAdmin, PermExercisesPublish, true},
		{RoleAdmin, PermExercisesDelete, true},
		{RoleAdmin, PermExercisesExport, true},
		{RoleAdmin, PermExercisesElevationsRead, false}, // resource elevations are decided by super_admin only
		{RoleAdmin, PermExercisesElevationsWrite, false},
		{RoleAdminViewer, PermExercisesElevationsRead, false},
		{RoleSuperAdmin, PermExercisesElevationsWrite, true},
		{RoleAdminViewer, PermExercisesRead, true},
		{RoleAdminViewer, PermExercisesExport, false},
		{RoleAdminViewer, PermExercisesWrite, false},
		{RoleAdminViewer, PermExercisesPublish, false},
		{RoleAdminViewer, PermExercisesDelete, false},
		{RoleUser, PermExercisesRead, false},
		{RoleUser, "users.read", false},   // not in held set
		{RolePublic, "users.read", false}, // unknown/none → fail-closed
		// notifications.* permission cases
		{
			RoleAdmin,
			"notifications.templates.write",
			false,
		}, // notification mgmt is super_admin-only
		{RoleAdmin, "notifications.settings.write", false},
		{RoleAdmin, "notifications.test", false},
		{RoleAdmin, "notifications.self", true},
		{RoleAdminViewer, "notifications.templates.read", false}, // viewer loses notification mgmt
		{
			RoleAdminViewer,
			"notifications.templates.write",
			false,
		}, // viewer holds no notification-management perms
		{RoleAdminViewer, "notifications.settings.read", false},
		{RoleAdminViewer, "notifications.settings.write", false},
		{RoleAdminViewer, "notifications.self", true},
		{RoleAdminViewer, "notifications.test", false},     // viewer cannot send test sends
		{RoleUser, "notifications.self", true},             // user covers own inbox/settings
		{RoleUser, "notifications.templates.read", false},  // user cannot read templates
		{RoleUser, "notifications.templates.write", false}, // user cannot mutate templates
		{RoleUser, "notifications.settings.read", false},   // user cannot read global settings
		{RoleUser, "notifications.settings.write", false},  // user cannot mutate global settings
	}
	for _, c := range cases {
		if got := HasPermission(c.role, c.required); got != c.want {
			t.Errorf("HasPermission(%q,%q)=%v want %v", c.role, c.required, got, c.want)
		}
	}
}

func TestCanAssignRole(t *testing.T) {
	cases := []struct {
		caller, target Role
		want           bool
	}{
		{RoleSuperAdmin, RoleSuperAdmin, true},
		{RoleSuperAdmin, RoleAdmin, true},
		{RoleAdmin, RoleSuperAdmin, false}, // cannot grant "*"
		{RoleAdmin, RoleAdmin, true},       // own perms covered
		{RoleAdmin, RoleAdminViewer, true},
		{RoleAdmin, RoleUser, true}, // empty target always assignable
		{RoleAdminViewer, RoleAdmin, false},
		{RoleAdminViewer, RoleUser, true},
		{RoleAdminViewer, RoleAdminViewer, true},
	}
	for _, c := range cases {
		if got := CanAssignRole(c.caller, c.target); got != c.want {
			t.Errorf("CanAssignRole(%q,%q)=%v want %v", c.caller, c.target, got, c.want)
		}
	}
}

func TestCovers(t *testing.T) {
	if !covers("*", "anything") {
		t.Error("* must cover anything")
	}
	if !covers("users", "users.read") {
		t.Error("parent must cover child")
	}
	if covers("users.read", "users") {
		t.Error("child must NOT cover parent")
	}
	if covers("user", "users.read") {
		t.Error("prefix without dot boundary must NOT cover")
	}
	if !covers("events.solution-attempts.write", "events.solution-attempts.read") {
		t.Error("write must cover read for the same resource")
	}
	if covers("events.solution-attempts.read", "events.solution-attempts.write") {
		t.Error("read must not cover write")
	}
	if covers("events.write", "events.solution-attempts.read") {
		t.Error("write must not cover a different nested resource")
	}
}

func TestPermissionStrings(t *testing.T) {
	contains := func(xs []string, want string) bool {
		for _, x := range xs {
			if x == want {
				return true
			}
		}
		return false
	}
	if s := PermissionStrings(RoleSuperAdmin); len(s) != 1 || s[0] != "*" {
		t.Fatalf("super_admin want [*], got %v", s)
	}
	a := PermissionStrings(RoleAdmin)
	if !contains(a, "users") || !contains(a, "notifications.self") ||
		contains(a, "platform.settings") {
		t.Fatalf("admin perms wrong: %v", a)
	}
	if !contains(a, string(PermEventsRead)) || !contains(a, string(PermEventsWrite)) {
		t.Fatalf("admin must receive explicit catalog read/write permissions for the UI: %v", a)
	}
	for _, permission := range []Permission{PermExercisesRead, PermExercisesWrite, PermExercisesPublish, PermExercisesDelete, PermExercisesExport} {
		if !contains(a, string(permission)) {
			t.Fatalf("admin missing exercise permission %q: %v", permission, a)
		}
	}
	viewer := PermissionStrings(RoleAdminViewer)
	if !contains(viewer, string(PermExercisesRead)) || contains(viewer, string(PermExercisesWrite)) ||
		contains(viewer, string(PermExercisesPublish)) || contains(viewer, string(PermExercisesDelete)) ||
		contains(viewer, string(PermExercisesExport)) {
		t.Fatalf("admin_viewer exercise permissions wrong: %v", viewer)
	}
	u := PermissionStrings(RoleUser)
	if !contains(u, "notifications.self") || !contains(u, "self") {
		t.Fatalf("user should hold notifications.self + self, got %v", u)
	}
	if contains(u, "users") || contains(u, "*") {
		t.Fatalf("user must not hold admin perms, got %v", u)
	}
	if len(PermissionStrings("nonsense")) != 0 {
		t.Fatalf("unknown role should hold nothing")
	}
}

func TestPlatformMailSettingsAreSuperAdminOnly(t *testing.T) {
	for _, perm := range []Permission{PermPlatformSettingsRead, PermPlatformSettingsWrite} {
		if !HasPermission(RoleSuperAdmin, perm) {
			t.Fatalf("super_admin must hold %s", perm)
		}
		for _, role := range []Role{RoleAdmin, RoleAdminViewer, RoleUser} {
			if HasPermission(role, perm) {
				t.Fatalf("%s must not hold %s (platform mail sender, reply-to and SMTP)", role, perm)
			}
		}
	}
}

func TestCanSetRole(t *testing.T) {
	cases := []struct {
		caller, current, target Role
		want                    bool
	}{
		// a super_admin sets anything but is the only one who makes admins
		{RoleSuperAdmin, RoleUser, RoleAdmin, true},
		{RoleSuperAdmin, RoleAdmin, RoleSuperAdmin, true},
		// an admin never raises anyone to admin or above
		{RoleAdmin, RoleUser, RoleAdmin, false},
		{RoleAdmin, RoleAdminViewer, RoleAdmin, false},
		{RoleAdmin, "", RoleAdmin, false}, // a new account (invitation)
		{RoleAdmin, RoleAdmin, RoleSuperAdmin, false},
		// an admin lowers another admin, or leaves one as it is
		{RoleAdmin, RoleAdmin, RoleAdminViewer, true},
		{RoleAdmin, RoleAdmin, RoleUser, true},
		{RoleAdmin, RoleAdmin, RoleAdmin, true},
		// below admin an admin may set freely
		{RoleAdmin, RoleUser, RoleAdminViewer, true},
		{RoleAdmin, "", RoleUser, true},
		{RoleAdmin, RoleAdminViewer, RoleUser, true},
		// those without the permissions cannot grant anything above their own
		{RoleAdminViewer, RoleUser, RoleAdminViewer, true},
		{RoleUser, RoleUser, RoleAdminViewer, false},
	}
	for _, c := range cases {
		if got := CanSetRole(c.caller, c.current, c.target); got != c.want {
			t.Errorf("CanSetRole(%q,%q,%q)=%v want %v", c.caller, c.current, c.target, got, c.want)
		}
	}
}
