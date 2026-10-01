// Package eventManagerModel owns event-local management membership. It is
// deliberately separate from global RBAC: a valid platform session must still
// be granted a role for the concrete event it is trying to administer.
package eventManagerModel

import (
	"time"

	"github.com/gofrs/uuid"
)

type Role int16

const (
	RoleOwner Role = iota
	RoleManager
	RoleViewer
)

func (r Role) valid() bool {
	return r == RoleOwner || r == RoleManager || r == RoleViewer
}

// EventManager is one user's management role in one event. The pair
// (EventID, UserID) is its persistence key.
type EventManager struct {
	EventID   uuid.UUID
	UserID    uuid.UUID
	Role      Role
	CreatedAt time.Time
}

func New(eventID, userID uuid.UUID, role Role, now time.Time) (EventManager, error) {
	if eventID == uuid.Nil || userID == uuid.Nil {
		return EventManager{}, ErrEventManagerIdentityInvalid.Err()
	}
	if !role.valid() {
		return EventManager{}, ErrEventManagerRoleInvalid.Err()
	}
	return EventManager{EventID: eventID, UserID: userID, Role: role, CreatedAt: now}, nil
}

// CanManage reports whether this membership may change its event. Viewers can
// be granted read access later but are never permitted to mutate state.
func (m EventManager) CanManage() bool {
	return m.Role == RoleOwner || m.Role == RoleManager
}

// CanRead permits every valid event-local membership to inspect management
// data. RoleViewer deliberately stops here and cannot mutate event state.
func (m EventManager) CanRead() bool {
	return m.Role == RoleOwner || m.Role == RoleManager || m.Role == RoleViewer
}
