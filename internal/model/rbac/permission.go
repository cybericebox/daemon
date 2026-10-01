package rbac

import "strings"

// Permission is a dotted-namespace capability string. A held permission covers
// a required one when they are equal, the held one is "*", or the required one
// lives beneath the held one in the dotted hierarchy (held + "." prefix).
type Permission string

const (
	// PermAll is the wildcard permission: it covers every required permission.
	// Held only by super_admin.
	PermAll Permission = "*"

	// PermUsers is the parent that covers all users.* permissions.
	PermUsers            Permission = "users"
	PermUsersRead        Permission = "users.read"
	PermUsersRoleWrite   Permission = "users.role.write"
	PermUsersStatusWrite Permission = "users.status.write"
	PermUsersDelete      Permission = "users.delete"
	PermUsersInvite      Permission = "users.invite"

	// PermPlatformSettings is the platform.settings.* namespace parent. Held by no
	// role today (super_admin reaches platform settings via "*"); kept for the hierarchy.
	PermPlatformSettings          Permission = "platform.settings"
	PermPlatformSettingsRead      Permission = "platform.settings.read"
	PermPlatformSettingsReadValue Permission = "platform.settings.read.value"
	PermPlatformSettingsWrite     Permission = "platform.settings.write"
	PermPlatformAuditRead         Permission = "platform.audit.read"

	// PermNotifications is the notifications.* namespace parent. Held by no role today
	// (super_admin reaches notification management via "*"); kept for the hierarchy.
	PermNotifications Permission = "notifications"
	// Notification permissions.
	// PermNotificationsSelf covers a user's own inbox and own per-user settings.
	PermNotificationsSelf Permission = "notifications.self"
	// PermNotificationsTemplatesRead allows reading notification templates.
	PermNotificationsTemplatesRead Permission = "notifications.templates.read"
	// PermNotificationsTemplatesWrite allows creating/updating/deleting templates.
	PermNotificationsTemplatesWrite Permission = "notifications.templates.write"
	// PermNotificationsSettingsRead allows reading global notification settings.
	PermNotificationsSettingsRead Permission = "notifications.settings.read"
	// PermNotificationsSettingsWrite allows writing global notification settings.
	PermNotificationsSettingsWrite Permission = "notifications.settings.write"
	// PermNotificationsTest allows sending test notifications.
	PermNotificationsTest Permission = "notifications.test"
	// PermNotificationsBroadcast allows composing, sending and listing custom
	// platform broadcasts.
	PermNotificationsBroadcast Permission = "notifications.broadcast"
	// PermNotificationsBannersRead / Write allow listing and managing the
	// platform site banners.
	PermNotificationsBannersRead  Permission = "notifications.banners.read"
	PermNotificationsBannersWrite Permission = "notifications.banners.write"
	// PermBannersView is the optional-session read of the banners a viewer
	// sees (public: anonymous viewers get the audience "everyone" banners).
	PermBannersView Permission = "banners.view"

	// PermExercises is the exercises.* namespace parent. Held by no role today
	// (super_admin reaches the catalog via "*"); kept for the hierarchy.
	PermExercises Permission = "exercises"
	// PermExercisesRead allows reading the catalog: lists, cards, versions, files.
	PermExercisesRead Permission = "exercises.read"
	// PermExercisesWrite allows creating exercises, editing identity, saving
	// drafts and uploading files.
	PermExercisesWrite Permission = "exercises.write"
	// PermExercisesPublish allows publish/rollback/discard — deliberately
	// separate from write: editing content and releasing it are two rights.
	PermExercisesPublish Permission = "exercises.publish"
	// PermExercisesDelete allows deleting catalog entries.
	PermExercisesDelete Permission = "exercises.delete"
	// PermExercisesExport allows exporting catalog entries, including decrypted
	// secrets when requested. Reading catalog details alone does not grant it.
	PermExercisesExport Permission = "exercises.export"

	// PermEvents is the events.* namespace parent. Held by no role today
	// (super_admin reaches it via "*"), mirroring exercises.
	PermEvents Permission = "events"
	// PermEventsRead allows listing and reading events.
	PermEventsRead Permission = "events.read"
	// PermEventResultsRead permits an optional-session event result read. The
	// event configuration still decides whether a particular caller may see it.
	PermEventResultsRead Permission = "events.results.read"
	// PermEventContentRead permits an optional-session read of event landing and
	// static content. The event/page visibility remains enforced by its use case.
	PermEventContentRead Permission = "events.content.read"
	// PermEventsWrite allows creating, updating, archiving and deleting events.
	PermEventsWrite Permission = "events.write"
	// PermEventsSolutionAttemptsRead grants access to submitted answers and
	// expected flags in the privileged solution-attempt audit log.
	PermEventsSolutionAttemptsRead Permission = "events.solution-attempts.read"
	// PermEventsSolutionAttemptsWrite grants manual accept/reject/revert
	// decisions over submitted event answers.
	PermEventsSolutionAttemptsWrite Permission = "events.solution-attempts.write"

	// PermInfrastructure is the infrastructure.* namespace parent (held by no role
	// today; super_admin reaches it via "*"), mirroring exercises/events.
	PermInfrastructure Permission = "infrastructure"
	// PermInfrastructureRead allows viewing the cyber-range infrastructure
	// availability and operational telemetry (agents, laboratories, capacity).
	// This is platform-operator information, restricted to super_admin.
	PermInfrastructureRead Permission = "infrastructure.read"
	// PermInfrastructureWrite allows changing running infrastructure (recreating
	// a team stand from the admin panel). Platform-operator only: super_admin.
	PermInfrastructureWrite Permission = "infrastructure.write"

	// PermAnalytics is the analytics.* namespace parent (held by no role today;
	// super_admin reaches it via "*"), mirroring exercises/events.
	PermAnalytics Permission = "analytics"
	// PermAnalyticsRead allows the platform-level analytics: aggregate numbers
	// across events, users, the task catalog, infrastructure and mail.
	PermAnalyticsRead Permission = "analytics.read"
	// PermAnalyticsUsersRead allows the per-user rows of the users analytics
	// (individual accounts). Super_admin only, deliberately not covered by
	// analytics.read.
	PermAnalyticsUsersRead Permission = "analytics.users.read"

	// PermSelf is a flat, cross-cutting permission — not part of any resource's
	// dotted namespace (users.*, notifications.*, ...) because it isn't about a
	// resource area. It means "this caller is an authenticated ordinary user
	// acting on things scoped to their own account" (sign-out, own sessions,
	// own account/profile/avatar, own password, own Google link). Held by every
	// authenticated role; RolePublic does not hold it, so attaching
	// RequirePermission(PermSelf) to a route is sufficient on its own — no
	// separate "require authentication" middleware is needed.
	PermSelf Permission = "self"
)

func (p *Permission) String() string {
	return string(*p)
}

func (p *Permission) RequireAuthentication() bool {
	return RolePublic.HasPermission(*p) == false
}

// rolePermissions is the static role → held-permission map. admin manages
// users, events, and exercises; admin_viewer can read them. Platform settings
// and notification management are super_admin-only (reachable only via "*").
var rolePermissionsSet = map[Role]permissionSet{
	RoleSuperAdmin:  newRolePerms(PermAll),
	RoleAdmin:       newRolePerms(PermUsers, PermEventsRead, PermEventsWrite, PermExercisesRead, PermExercisesWrite, PermExercisesPublish, PermExercisesDelete, PermExercisesExport, PermPlatformAuditRead, PermEventsSolutionAttemptsRead, PermEventsSolutionAttemptsWrite).InheritRoles(RoleUser),
	RoleAdminViewer: newRolePerms(PermUsersRead, PermEventsRead, PermExercisesRead, PermEventsSolutionAttemptsRead).InheritRoles(RoleUser),
	RoleUser:        newRolePerms(PermNotificationsSelf, PermSelf).InheritRoles(RolePublic),
	RolePublic:      newRolePerms(PermPlatformSettingsReadValue, PermEventResultsRead, PermEventContentRead, PermBannersView),
}

var rolePermissions = map[Role][]Permission{}

// init resolves InheritRoles chains recursively (with memoization) into the
// flat rolePermissions map that Role.Permissions reads.
func init() {
	var resolve func(r Role, seen map[Role]bool) []Permission
	resolve = func(r Role, seen map[Role]bool) []Permission {
		if perms, done := rolePermissions[r]; done {
			return perms
		}
		if seen[r] {
			panic("rbac: inheritance cycle at role " + string(r))
		}
		seen[r] = true
		set := rolePermissionsSet[r]
		perms := append([]Permission{}, set.permissions...)
		for _, parent := range set.inherits {
			perms = append(perms, resolve(parent, seen)...)
		}
		rolePermissions[r] = perms
		return perms
	}
	for role := range rolePermissionsSet {
		resolve(role, map[Role]bool{})
	}
}

// covers reports whether a held permission grants a required one.
func covers(held, required Permission) bool {
	if held == PermAll || held == required || strings.HasPrefix(string(required), string(held)+".") {
		return true
	}
	// A write capability includes reading the exact same resource. Keep this
	// deliberately symmetric only by resource stem: e.g. events.x.write
	// covers events.x.read, but events.write does not open events.x.read.
	heldName, requiredName := string(held), string(required)
	return strings.HasSuffix(heldName, ".write") &&
		strings.HasSuffix(requiredName, ".read") &&
		strings.TrimSuffix(heldName, ".write") == strings.TrimSuffix(requiredName, ".read")
}

// HasPermission reports whether the role holds a permission that covers
// required. Unknown roles hold nothing (fail-closed).
func HasPermission(role Role, required Permission) bool {
	return role.HasPermission(required)
}

// CanAssignRole reports whether caller may assign target: every permission the
// target role grants must be covered by the caller's held set. Consequence:
// super_admin ("*") is assignable only by super_admin.
func CanAssignRole(caller, target Role) bool {
	for _, p := range target.Permissions() {
		if !caller.HasPermission(p) {
			return false
		}
	}
	return true
}

// PermissionStrings returns the held permission set for a role as plain
// strings, for the API to expose to clients. Mirrors Permissions.
func PermissionStrings(role Role) []string {
	held := role.Permissions()
	out := make([]string, 0, len(held))
	for _, p := range held {
		out = append(out, string(p))
	}
	return out
}
