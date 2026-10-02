package dispatchModel

import (
	"encoding/json"
	"errors"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// Payload is what a queued notification needs besides ids: the template variables, the recipient
// override and the inbox metadata. It is sealed in the database, never put into the River job
// arguments.
type Payload struct {
	Vars      json.RawMessage  `json:"vars,omitempty"`
	Recipient *userModel.User  `json:"recipient,omitempty"`
	Inbox     *inboxModel.Meta `json:"inbox,omitempty"`
}

// ErrPayloadGone: the sealed payload of a queued notification no longer exists (expired, or the
// account was deleted). The job cannot run and must not be retried.
var ErrPayloadGone = errors.New("notification payload is gone")
