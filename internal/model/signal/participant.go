package signalModel

import "github.com/gofrs/uuid"

const (
	TypeParticipantApprovalRegistrationSubmitted Type = "participant.approval_registration.submitted"
	TypeParticipantApprovalRegistrationApproved  Type = "participant.approval_registration.approved"
	TypeParticipantApprovalRegistrationRejected  Type = "participant.approval_registration.rejected"
	TypeParticipantOpenRegistrationCompleted     Type = "participant.open_registration.completed"
	TypeParticipantInvitationSent                Type = "participant.invitation.sent"
	TypeParticipantTeamInvitationSent            Type = "participant.team_invitation.sent"
	TypeParticipantInvitationAccepted            Type = "participant.invitation.accepted"
	TypeParticipantInvitationDeclined            Type = "participant.invitation.declined"
	TypeParticipantInvitationRevoked             Type = "participant.invitation.revoked"
	TypeParticipantInvitationExpired             Type = "participant.invitation.expired"
	TypeParticipantEnrolled                      Type = "participant.enrolled"
)

// RegistrationKind records the immutable registration route which led to a
// participant signal. It lets templates distinguish open completion, approval
// flow, and later invitation acceptance without rereading mutable config.
type RegistrationKind string

const (
	RegistrationOpen       RegistrationKind = "open"
	RegistrationApproval   RegistrationKind = "approval"
	RegistrationInvitation RegistrationKind = "invitation"
)

// ParticipantPayload is the snapshot shared by the initial registration
// signal catalogue. It deliberately contains only the stable event identity,
// user routing, and text required by a recipient-specific notification filler.
type ParticipantPayload struct {
	ScopeEventID  uuid.UUID        `json:"scope_event_id"`
	ActorUserID   uuid.UUID        `json:"actor_user_id,omitempty"`
	SubjectUserID uuid.UUID        `json:"subject_user_id"`
	EventTag      string           `json:"event_tag"`
	EventName     string           `json:"event_name"`
	Registration  RegistrationKind `json:"registration"`
}

func (p *ParticipantPayload) Routing() Routing {
	routing := Routing{ScopeEventID: p.ScopeEventID}
	if p.ActorUserID != uuid.Nil {
		actor := p.ActorUserID
		routing.ActorUserID = &actor
	}
	if p.SubjectUserID != uuid.Nil {
		subject := p.SubjectUserID
		routing.SubjectUserID = &subject
	}
	return routing
}

// DefaultRegistry is the application-wide code-owned catalogue. Every durable
// signal must register a constructor before the worker can execute hooks.
var DefaultRegistry = newDefaultRegistry()

func newDefaultRegistry() *Registry {
	registry := NewRegistry()
	registry.Register(TypeEventManagerAssigned, func() Payload { return &EventManagerPayload{} })
	registry.Register(TypeEventLabFailed, func() Payload { return &EventLabFailedPayload{} })
	registry.Register(TypeParticipantEventStartReminder, func() Payload { return &EventNoticePayload{} })
	registry.Register(TypeParticipantEventFinished, func() Payload { return &EventNoticePayload{} })
	registry.Register(TypeParticipantEventResultsPublished, func() Payload { return &EventNoticePayload{} })
	for _, typ := range []Type{
		TypeParticipantApprovalRegistrationSubmitted,
		TypeParticipantApprovalRegistrationApproved,
		TypeParticipantApprovalRegistrationRejected,
		TypeParticipantOpenRegistrationCompleted,
		TypeParticipantInvitationSent,
		TypeParticipantTeamInvitationSent,
		TypeParticipantInvitationAccepted,
		TypeParticipantInvitationDeclined,
		TypeParticipantInvitationRevoked,
		TypeParticipantInvitationExpired,
		TypeParticipantEnrolled,
	} {
		registry.Register(typ, func() Payload { return &ParticipantPayload{} })
	}
	return registry
}
