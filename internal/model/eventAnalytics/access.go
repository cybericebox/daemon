// Package eventAnalyticsModel owns the event analytics rules
// (docs/EVENT-ANALYTICS.md): who may see which report (§7), how VPN sessions
// are derived from handshakes (D5) and when the rollups of an event are
// final (§5).
package eventAnalyticsModel

import (
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Access is what one viewer may see in the analytics of one event.
type Access struct {
	// Sections: every analytics section and the event report on the screen.
	Sections bool
	// Sensitive: individual-level views — the text of wrong answers and the
	// integrity section.
	Sensitive bool
	// Export: the CSV exports and the report download. Data leaves the system
	// only with the owner, write moderators and platform admins; a read-only
	// moderator (observer) and an admin viewer read it on the screen.
	Export bool
}

// ResolveAccess applies §7. The event owner and write moderators see and
// export everything; read-only moderators see the sections only and export
// nothing. Platform staff
// follow their platform role: events.write (admins) like a write moderator,
// events.read (admin viewers) like a read-only moderator. membership is nil
// when the viewer has no role in the event.
func ResolveAccess(membership *eventManagerModel.EventManager, role rbac.Role) Access {
	var access Access
	if membership != nil {
		access.Sections = membership.CanRead()
		access.Sensitive = membership.CanManage()
		access.Export = membership.CanManage()
	}
	if role.HasPermission(rbac.PermEventsWrite) {
		access.Sections, access.Sensitive, access.Export = true, true, true
	} else if role.HasPermission(rbac.PermEventsRead) {
		access.Sections = true
	}
	return access
}
