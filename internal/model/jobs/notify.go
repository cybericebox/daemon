package jobsModel

import (
	"encoding/json"

	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// NotifyArgs is the River job envelope, declared beside the worker that
// consumes it. Vars            are JSON here (transport form). The id fields are gofrs
// uuid.UUID, which round-trip to JSON as strings and pass straight into
// dispatcher.ProcessInput.
type NotifyArgs struct {
	DispatchID       uuid.UUID        `json:"dispatch_id"`
	UserID           uuid.UUID        `json:"user_id"`
	Type             string           `json:"type"`
	Vars             json.RawMessage  `json:"vars"`
	OverrideChannels []string         `json:"override_channels,omitempty"`
	Recipient        *userModel.User  `json:"recipient,omitempty"`
	TemplateID       *uuid.UUID       `json:"template_id,omitempty"`
	ScopeEventID     *uuid.UUID       `json:"scope_event_id,omitempty"`
	Inbox            *inboxModel.Meta `json:"inbox,omitempty"`
	BroadcastID      *uuid.UUID       `json:"broadcast_id,omitempty"`
}

func (NotifyArgs) Kind() string { return "notify" }
