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

// ReadPermissions are the access levels a generic setting may carry in RequiredPermission. No public site reads
// platform settings, so every level is an administrator's: "" (the default) and platform.settings.read are
// platform.settings.read; platform.settings.write narrows a sensitive setting to those who may also change it.
// Anything else is refused on save.
var ReadPermissions = []string{"", "platform.settings.read", "platform.settings.write"}

// ValidReadPermission reports whether p is one of ReadPermissions.
func ValidReadPermission(p string) bool {
	for _, known := range ReadPermissions {
		if p == known {
			return true
		}
	}
	return false
}

// ReadPermission is the permission a caller needs to read s: an empty RequiredPermission means an administrator
// (platform.settings.read), never anonymous.
func (s PlatformSetting) ReadPermission() string {
	if s.RequiredPermission == "" {
		return "platform.settings.read"
	}
	return s.RequiredPermission
}
