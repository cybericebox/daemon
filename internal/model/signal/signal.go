// Package signalModel defines durable system facts emitted by domain use cases.
package signalModel

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"
)

// Type identifies a stable system signal. Events in the product remain
// competitions; signals are facts that hooks may consume.
type Type string

// Signal is the durable envelope stored by the outbox. Domain-specific routing
// and immutable context belong to Payload, not this generic envelope.
type Signal struct {
	ID         uuid.UUID
	Type       Type
	OccurredAt time.Time
	Payload    json.RawMessage
}

// Routing exposes only the references needed by generic hook infrastructure.
type Routing struct {
	ScopeEventID  uuid.UUID
	ActorUserID   *uuid.UUID
	SubjectUserID *uuid.UUID
	TeamID        *uuid.UUID
}

// Payload is a typed signal body. Implementations must be JSON serializable.
type Payload interface {
	Routing() Routing
}

var ErrUnknownType = errors.New("signal: unknown type")
