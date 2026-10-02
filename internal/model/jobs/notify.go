package jobsModel

import (
	"encoding/json"

	"github.com/gofrs/uuid"

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
	DispatchID       uuid.UUID  `json:"dispatch_id"`
	UserID           uuid.UUID  `json:"user_id"`
	Type             string     `json:"type"`
	OverrideChannels []string   `json:"override_channels,omitempty"`
	TemplateID       *uuid.UUID `json:"template_id,omitempty"`
	ScopeEventID     *uuid.UUID `json:"scope_event_id,omitempty"`
	BroadcastID      *uuid.UUID `json:"broadcast_id,omitempty"`

	// Deprecated: legacy jobs only.
	Vars      json.RawMessage  `json:"vars,omitempty"`
	Recipient *userModel.User  `json:"recipient,omitempty"`
	Inbox     *inboxModel.Meta `json:"inbox,omitempty"`
}

func (NotifyArgs) Kind() string { return "notify" }
