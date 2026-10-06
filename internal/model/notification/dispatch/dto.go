package dispatchModel

import (
	"time"

	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// NotifyOptions holds optional overrides that callers can pass to Notify.
type NotifyOptions struct {
	Recipient        *userModel.User
	OverrideChannels []notificationTypes.NotificationChannel
	TemplateID       *uuid.UUID
	ScopeEventID     *uuid.UUID
	Inbox            *inboxModel.Meta
	BroadcastID      *uuid.UUID
}

// NotifyOption is a functional option for NotifyOptions.
type NotifyOption func(*NotifyOptions)

// WithRecipient overrides the notification recipient with a full user model.
// The caller fills only the fields its target channels need (email → Email,
// in-app → ID). The dispatch row still references the real userID.
func WithRecipient(u userModel.User) NotifyOption {
	return func(o *NotifyOptions) { o.Recipient = &u }
}

// WithOverrideChannels forces a specific channel set, bypassing preference resolution.
func WithOverrideChannels(chs ...notificationTypes.NotificationChannel) NotifyOption {
	return func(o *NotifyOptions) { o.OverrideChannels = chs }
}

// WithTemplateID overrides the template used for rendering. When set, channels should
// load the specific template by ID instead of the published one for the notification
// type. Consumed downstream starting SP4 (see process.go TODO).
func WithTemplateID(id uuid.UUID) NotifyOption {
	return func(o *NotifyOptions) { o.TemplateID = &id }
}

// WithEventScope selects an event-specific template when one exists and falls
// back to the platform default otherwise.
func WithEventScope(id uuid.UUID) NotifyOption {
	return func(o *NotifyOptions) { o.ScopeEventID = &id }
}

// WithInbox classifies the in-app copy (category, request subject). Without
// it the in-app handler classifies the type for the subject role.
func WithInbox(meta inboxModel.Meta) NotifyOption {
	return func(o *NotifyOptions) { o.Inbox = &meta }
}

// WithBroadcast marks the dispatch as one recipient of a custom broadcast: the
// channels render the broadcast content instead of a stored template.
func WithBroadcastID(id uuid.UUID) NotifyOption {
	return func(o *NotifyOptions) { o.BroadcastID = &id }
}

// ApplyNotifyOptions applies all functional options and returns the resulting NotifyOptions.
func ApplyNotifyOptions(opts []NotifyOption) NotifyOptions {
	var o NotifyOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// ProcessInput is the domain input to ProcessNotification (no River, no JSON envelope).
type ProcessInput struct {
	DispatchID       uuid.UUID
	UserID           uuid.UUID
	Type             string
	Vars             map[string]any
	OverrideChannels []notificationTypes.NotificationChannel
	Recipient        *userModel.User // when set, used verbatim instead of fetching by UserID
	TemplateID       *uuid.UUID      // when set, render using this specific template (draft preview)
	ScopeEventID     *uuid.UUID      // optional event template scope; handlers fall back to the platform template
	Inbox            *inboxModel.Meta
	BroadcastID      *uuid.UUID // when set, the channels render this broadcast's content
}

// --- admin read DTOs (notification logs + statistics) ---

// DispatchInfo is one row in the dispatch log.
type DispatchInfo struct {
	ID               uuid.UUID
	NotificationType string
	RecipientUserID  uuid.UUID
	RecipientEmail   string
	RecipientName    string
	ScopeEventID     *uuid.UUID
	EventName        string
	BroadcastID      *uuid.UUID
	Status           string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// DispatchTarget is a per-channel delivery result within a dispatch.
type DispatchTarget struct {
	Channel  string
	Status   string
	Error    string
	Attempts int32
	// Transport is the email route that made the last attempt (event,
	// platform, env); FallbackError is the Event SMTP error that sent the
	// message to the platform transport.
	Transport     string
	Recipient     string
	FallbackError string
	UpdatedAt     time.Time
}

// DispatchDetail is a dispatch plus its targets.
type DispatchDetail struct {
	DispatchInfo
	Targets []DispatchTarget
}

// KeyCount is a generic labelled count (status→n, type→n).
type KeyCount struct {
	Key   string
	Count int64
}

// ChannelStatusCount is a per-channel, per-status target count.
type ChannelStatusCount struct {
	Channel string
	Status  string
	Count   int64
}

// Stats is the statistics aggregate over a time window.
type Stats struct {
	Since     time.Time
	Total     int64
	ByStatus  []KeyCount
	ByType    []KeyCount
	ByChannel []ChannelStatusCount
}

// ListDispatchesFilter is the dispatch-log query filter.
type ListDispatchesFilter struct {
	Type   string
	Status string
	User   string // UUID string; "" = no filter
	Event  string // UUID string; "" = no filter
	// Channel, Result (target status) and Transport match a single target.
	Channel   string
	Result    string
	Transport string
	Cursor    string // last dispatch ID from the previous page; "" = first page
	Limit     int32
}
