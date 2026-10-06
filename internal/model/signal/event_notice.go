package signalModel

import "github.com/gofrs/uuid"

// Event-wide participant notices (W7). They carry no subject: the planner
// resolves the audience (all approved participants by default).
const (
	TypeParticipantEventStartReminder Type = "participant.event.start_reminder"
	TypeParticipantEventFinished      Type = "participant.event.finished"
	// TypeParticipantEventResultsPublished: a moderator opened the results
	// («Відкрити підсумки»).
	TypeParticipantEventResultsPublished Type = "participant.event.results_published"
)

// EventNoticePayload snapshots the Event at emission time. Times are already
// formatted for Ukrainian recipients (Kyiv time).
type EventNoticePayload struct {
	ScopeEventID uuid.UUID `json:"scope_event_id"`
	EventTag     string    `json:"event_tag"`
	EventName    string    `json:"event_name"`
	EventURL     string    `json:"event_url"`
	StartAt      string    `json:"start_at,omitempty"`
	FinishAt     string    `json:"finish_at,omitempty"`
	HoursLeft    int       `json:"hours_left,omitempty"`
}

func (p *EventNoticePayload) Routing() Routing {
	return Routing{ScopeEventID: p.ScopeEventID}
}
