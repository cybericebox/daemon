package platformSettingsModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

type PlatformSetting struct {
	ID                 uuid.UUID
	Key                string
	Value              json.RawMessage
	RequiredPermission string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// NewPlatformSetting is the domain factory for a setting: id and timestamps
// come from here, not the database.
func NewPlatformSetting(key string, value json.RawMessage, requiredPermission string, now time.Time) PlatformSetting {
	return PlatformSetting{
		ID:                 uuid.Must(uuid.NewV7()),
		Key:                key,
		Value:              value,
		RequiredPermission: requiredPermission,
		CreatedAt:          now,
		UpdatedAt:          now,
	}
}
