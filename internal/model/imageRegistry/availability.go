package imageRegistryModel

// Status is the result of an image availability check.
type Status string

const (
	StatusAvailable   Status = "available"
	StatusUnavailable Status = "unavailable"
)

// Reason explains an unavailable status. It never carries a token.
type Reason string

const (
	ReasonNotConfigured    Reason = "not_configured"
	ReasonNotFound         Reason = "not_found"
	ReasonUnauthorized     Reason = "unauthorized"
	ReasonUnreachable      Reason = "unreachable"
	ReasonNeedsEventSecret Reason = "needs_event_secret"
	ReasonInvalidReference Reason = "invalid_reference"
)

// Availability is the check result for one image reference.
type Availability struct {
	Image  string
	Status Status
	Reason Reason
}
