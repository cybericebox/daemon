package signalModel

import "github.com/gofrs/uuid"

// TypeEventLabFailed is the urgent moderator notification about a team stand
// that could not be deployed. It is platform-owned (never event-overridable)
// and is published once per owner or moderator of the event.
const TypeEventLabFailed Type = "event.lab.failed"

// EventLabFailedPayload snapshots the failed stand. The subject is the
// recipient manager, so every manager gets an individual signal.
type EventLabFailedPayload struct {
	ScopeEventID  uuid.UUID `json:"scope_event_id"`
	SubjectUserID uuid.UUID `json:"subject_user_id"`
	EventTag      string    `json:"event_tag"`
	EventName     string    `json:"event_name"`
	TeamID        uuid.UUID `json:"team_id"`
	TeamName      string    `json:"team_name"`
	Reason        string    `json:"reason"`
}

func (p *EventLabFailedPayload) Routing() Routing {
	subject := p.SubjectUserID
	return Routing{ScopeEventID: p.ScopeEventID, SubjectUserID: &subject}
}
