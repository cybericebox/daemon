package signalModel

import "github.com/gofrs/uuid"

const TypeEventManagerAssigned Type = "event.manager.assigned"

// EventManagerPayload snapshots the event identity and newly granted role.
// The subject is the assignee, never the administrator who granted access.
type EventManagerPayload struct {
	ScopeEventID  uuid.UUID `json:"scope_event_id"`
	SubjectUserID uuid.UUID `json:"subject_user_id"`
	EventTag      string    `json:"event_tag"`
	EventName     string    `json:"event_name"`
	ManagerRole   string    `json:"manager_role"`
	RoleName      string    `json:"role_name"`
}

func (p *EventManagerPayload) Routing() Routing {
	subject := p.SubjectUserID
	return Routing{ScopeEventID: p.ScopeEventID, SubjectUserID: &subject}
}
