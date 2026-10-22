package inboxModel

import (
	"encoding/json"
	"errors"
	"strings"
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
	// Moot is set (at read time, not stored) on an open request whose object no longer waits for a decision:
	// its elevation request was removed or decided while the inbox copy stayed open. A recipient may close it.
	Moot bool
}

// IsOpenRequest reports whether the item still waits for an action.
func (n InAppNotification) IsOpenRequest() bool {
	return n.ActionRequired && n.ResolvedAt == nil
}

// Resolvable reports whether a recipient may close this open request by hand: its type has no domain decision
// of its own, or its object is gone (Moot).
func (n InAppNotification) Resolvable() bool {
	return n.IsOpenRequest() && (ManuallyResolvable(n.Type) || n.Moot)
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

var errInvalidCursor = errors.New("invalid inbox cursor") //nolint:gochecknoglobals // sentinel

// String is the wire form of the cursor: the creation time (RFC 3339, UTC, nanoseconds) and the id
// joined by an underscore. The zero cursor is the empty string.
func (c Cursor) String() string {
	if c.ID == uuid.Nil && c.CreatedAt.IsZero() {
		return ""
	}
	return c.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + c.ID.String()
}

// ParseCursor reads the wire form back. The empty string is no cursor.
func ParseCursor(raw string) (*Cursor, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // no cursor is a valid answer
	}
	at, id, found := strings.Cut(raw, "_")
	if !found {
		return nil, errInvalidCursor
	}
	createdAt, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, errInvalidCursor
	}
	itemID, err := uuid.FromString(id)
	if err != nil {
		return nil, errInvalidCursor
	}
	return &Cursor{ID: itemID, CreatedAt: createdAt}, nil
}

type Page struct {
	Items      []InAppNotification
	NextCursor *Cursor
}

// SinceResult is what happened in an inbox scope after a cursor.
type SinceResult struct {
	Cursor      *Cursor
	NewInbox    []InAppNotification
	UnreadCount int64
	Counts      Counts
	// OtherEventsCount is, for an Event-site scope, the unread items and open
	// requests of other Events (0 for every other scope).
	OtherEventsCount int64
}

// Pulse is the cheap summary of a user's inbox scope.
type Pulse struct {
	// Cursor is the newest inbox item, nil when the scope has none.
	Cursor *Cursor
	Unread int64
	// PersonalBanners and PersonalBannersLatest stamp the user's open personal banners.
	PersonalBanners       int64
	PersonalBannersLatest time.Time
}
