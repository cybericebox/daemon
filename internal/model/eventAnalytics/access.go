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
	// Sections: every analytics section and the event report download.
	Sections bool
	// Sensitive: individual-level views — the text of wrong answers and the
	// integrity section.
	Sensitive bool
}

// ResolveAccess applies §7. The event owner and write moderators see
// everything; read-only moderators see the sections only. Platform staff
// follow their platform role: events.write (admins) like a write moderator,
// events.read (admin viewers) like a read-only moderator. membership is nil
// when the viewer has no role in the event.
func ResolveAccess(membership *eventManagerModel.EventManager, role rbac.Role) Access {
	var access Access
	if membership != nil {
		access.Sections = membership.CanRead()
		access.Sensitive = membership.CanManage()
	}
	if role.HasPermission(rbac.PermEventsWrite) {
		access.Sections, access.Sensitive = true, true
	} else if role.HasPermission(rbac.PermEventsRead) {
		access.Sections = true
	}
	return access
}
