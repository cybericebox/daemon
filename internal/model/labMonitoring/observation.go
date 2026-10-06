// Package labMonitoring owns durable, secret-free Laboratory observations.
package labMonitoring

import (
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
)

type Observation struct {
	ID            uuid.UUID
	EventID       uuid.UUID
	EventTeamID   uuid.UUID
	LabGroupName  string
	AgentID       string
	Sequence      int64
	ObservedAt    time.Time
	ReceivedAt    time.Time
	SchemaVersion int32
	Snapshot      bool
	Payload       json.RawMessage
}

// NamedObservation is an Observation with the event and team names an
// administrator reads instead of ids. TeamName is empty for the hidden
// moderators team (clients label it).
type NamedObservation struct {
	Observation
	EventName  string
	TeamName   string
	Moderators bool
}

type EventTeam struct {
	EventID     uuid.UUID
	EventTeamID uuid.UUID
}

// CapacityObservation is cluster-wide, secret-free monitoring data. Unlike a
// LabGroup observation it must remain visible to platform administrators even
// when no event currently has a running team.
type CapacityObservation struct {
	ID            uuid.UUID
	AgentID       string
	Sequence      int64
	ObservedAt    time.Time
	ReceivedAt    time.Time
	SchemaVersion int32
	Snapshot      bool
	Payload       json.RawMessage
}
