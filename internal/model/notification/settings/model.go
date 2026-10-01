package settingsModel

import (
	"encoding/json"

	"github.com/gofrs/uuid"
)

type GlobalSetting struct {
	NotificationType string
	Channel          string
	Enabled          bool
	UserCanChange    bool
	UserDefault      bool
}

type UserSetting struct {
	UserID           uuid.UUID
	NotificationType string
	Channel          string
	Enabled          bool
}

// SignalDefault is the platform default subscription of one Event-scoped
// signal on one channel. Every Event inherits it unless it overrides the pair.
type SignalDefault struct {
	SignalType string
	Channel    string
	Enabled    bool
	Audience   json.RawMessage
}
