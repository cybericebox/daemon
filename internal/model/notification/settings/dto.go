package settingsModel

import (
	"github.com/gofrs/uuid"
)

type UpsertGlobalInput struct {
	NotificationType string
	Channel          string
	Enabled          bool
	UserCanChange    bool
	UserDefault      bool
}

type UpsertUserInput struct {
	UserID           uuid.UUID
	NotificationType string
	Channel          string
	Enabled          bool
}
