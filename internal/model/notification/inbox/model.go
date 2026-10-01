package inboxModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

// InAppNotification is the domain view of an in-app inbox entry. ReadAt is nil
// when the notification is unread.
type InAppNotification struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	Title         string
	Body          string
	Link          string
	Icon          string
	Tone          string
	AccentColor   string
	Surface       string
	AutoDismissMs *int32
	Actions       json.RawMessage
	Dismissible   bool
	ReadAt        *time.Time
	DismissedAt   *time.Time
	CreatedAt     time.Time
	// EventID is the Event the item belongs to (nil = account/system);
	// EventName and EventTag label it in platform-wide inboxes.
	EventID   *uuid.UUID
	EventName string
	EventTag  string
	// Type is the notification type; Category, ActionRequired and SubjectRef
	// are the inbox classification stored at insert (see Meta).
	Type           string
	Category       Category
	ActionRequired bool
	SubjectRef     string
	// ResolvedAt is set once the request's object was decided by anyone;
	// ResolvedByName labels ResolvedBy (empty for system resolutions).
	ResolvedAt     *time.Time
	Resolution     string
	ResolvedBy     *uuid.UUID
	ResolvedByName string
}

// IsOpenRequest reports whether the item still waits for an action.
func (n InAppNotification) IsOpenRequest() bool {
	return n.ActionRequired && n.ResolvedAt == nil
}

// PlatformScope is the inbox scope that lists only items without an Event
// (landing, ID, admin, catalog). No Event has the nil id, so the Event-site
// filter "that Event's items or items without an Event" reduces to the latter.
var PlatformScope = uuid.Nil

// Cursor uses database creation time and ID so it also works for older rows
// whose IDs were not generated as UUIDv7.
type Cursor struct {
	ID        uuid.UUID `json:"ID"`
	CreatedAt time.Time `json:"CreatedAt"`
}

type Page struct {
	Items      []InAppNotification
	NextCursor *Cursor
}

type PollResult struct {
	Cursor      *Cursor
	NewInbox    []InAppNotification
	UnreadCount int64
	Counts      Counts
	// OtherEventsCount is, for an Event-site scope, the unread items and open
	// requests of other Events (0 for every other scope).
	OtherEventsCount int64
}
