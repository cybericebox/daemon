package jobsModel

import (
	"encoding/json"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// NotifyArgs is the River job envelope. It carries ids and non-personal routing only: the variables,
// the recipient override and the inbox metadata (names, addresses, links) are sealed in a temporal
// code row keyed by DispatchID and read when the notification is sent.
//
// Vars, Recipient and Inbox are the legacy form (jobs queued before the sealed payload). They are
// read for such jobs and never written.
type NotifyArgs struct {
	// JournalDeliveryID routes a persisted operator delivery through this same notify queue.
	AuthDeliveryID    *uuid.UUID `json:"auth_delivery_id,omitempty"`
	JournalDeliveryID *uuid.UUID `json:"journal_delivery_id,omitempty"`
	DispatchID        uuid.UUID  `json:"dispatch_id"`
	UserID            uuid.UUID  `json:"user_id"`
	Type              string     `json:"type"`
	OverrideChannels  []string   `json:"override_channels,omitempty"`
	TemplateID        *uuid.UUID `json:"template_id,omitempty"`
	ScopeEventID      *uuid.UUID `json:"scope_event_id,omitempty"`
	BroadcastID       *uuid.UUID `json:"broadcast_id,omitempty"`

	// Deprecated: legacy jobs only.
	Vars      json.RawMessage  `json:"vars,omitempty"`
	Recipient *userModel.User  `json:"recipient,omitempty"`
	Inbox     *inboxModel.Meta `json:"inbox,omitempty"`
}

func (NotifyArgs) Kind() string { return "notify" }

// NotifyDurableQueue isolates the new tagged envelopes from older replicas during rolling updates.
// Old notify workers only consume default and would cancel these envelopes as missing legacy payloads.
const NotifyDurableQueue = "notify_durable_v1"

// NotifyDurableQueueWorkers is how many durable notification jobs run at once on one replica.
const NotifyDurableQueueWorkers = 2

func (a NotifyArgs) InsertOpts() river.InsertOpts {
	if a.JournalDeliveryID != nil || a.AuthDeliveryID != nil {
		return river.InsertOpts{Queue: NotifyDurableQueue}
	}
	return river.InsertOpts{Queue: river.QueueDefault}
}
