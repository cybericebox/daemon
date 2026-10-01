package platformSettingsUseCase

import "encoding/json"

// UpsertInput is the use-case input schema for create-or-update of a platform
// setting. Application-layer shape — lives with the use case, not the domain.
type UpsertInput struct {
	Key                string
	Value              json.RawMessage
	RequiredPermission string
}
