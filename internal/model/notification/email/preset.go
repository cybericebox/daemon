package emailModel

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

// BlockPreset is the domain representation of an email block preset.
type BlockPreset struct {
	ID          uuid.UUID
	Name        string
	Description string
	Blocks      json.RawMessage
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// PresetInput carries the mutable fields for create / update operations.
type PresetInput struct {
	Name        string
	Description string
	Blocks      json.RawMessage
}
