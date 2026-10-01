package notificationTypes

import (
	"encoding/json"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

// eventSignalPayload is the code-owned variable contract for every initial
// participant lifecycle signal. The planner creates the concrete per-recipient
// DefaultPayload; this prototype exists for channel support and editor-safe
// variable discovery before a planner instance is constructed.
type eventSignalPayload struct {
	Type          NotificationType
	ScopeEventID  string `var:"scope_event_id" desc:"Event identifier" default:"01900000-0000-7000-8000-000000000000"`
	ActorUserID   string `var:"actor_user_id" desc:"User who caused the event" default:"01900000-0000-7000-8000-000000000001"`
	SubjectUserID string `var:"subject_user_id" desc:"Participant affected by the event" default:"01900000-0000-7000-8000-000000000002"`
	EventTag      string `var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	EventName     string `var:"event_name" desc:"Event name" default:"Cyber ICE Box CTF"`
	EventURL      string `var:"event_url" desc:"Event site link" default:"https://ctf-2026.example.org/"`
	Registration  string `var:"registration" desc:"Registration route" default:"open"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"participant@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

type eventManagerSignalPayload struct {
	ScopeEventID  string `var:"scope_event_id" desc:"Event identifier" default:"01900000-0000-7000-8000-000000000000"`
	SubjectUserID string `var:"subject_user_id" desc:"User assigned to the event" default:"01900000-0000-7000-8000-000000000002"`
	EventTag      string `var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	EventName     string `var:"event_name" desc:"Event public name" default:"Cyber ICE Box CTF"`
	ManagerRole   string `var:"manager_role" desc:"Assigned event role code" default:"moderator"`
	RoleName      string `var:"role_name" desc:"Assigned event role label" default:"модератором"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"participant@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

// eventLabFailedSignalPayload is the variable contract of the moderator-facing
// laboratory failure signal «Лабораторія впала» (platform templates only).
type eventLabFailedSignalPayload struct {
	ScopeEventID  string `var:"scope_event_id" desc:"Event identifier" default:"01900000-0000-7000-8000-000000000000"`
	SubjectUserID string `var:"subject_user_id" desc:"Notified event manager" default:"01900000-0000-7000-8000-000000000002"`
	EventTag      string `var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	EventName     string `var:"event_name" desc:"Event public name" default:"Cyber ICE Box CTF"`
	TeamID        string `var:"team_id" desc:"Team identifier" default:"01900000-0000-7000-8000-000000000004"`
	TeamName      string `var:"team_name" desc:"Team name" default:"Blue Team"`
	Reason        string `var:"reason" desc:"Laboratory failure reason" default:"Laboratory reported phase Failed"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"moderator@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (eventLabFailedSignalPayload) NotificationType() NotificationType {
	return NotificationType(signalModel.TypeEventLabFailed)
}
func (eventLabFailedSignalPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelEmail, NotificationChannelInApp}
}
func (eventLabFailedSignalPayload) Marshal() (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (eventManagerSignalPayload) NotificationType() NotificationType {
	return NotificationType(signalModel.TypeEventManagerAssigned)
}
func (eventManagerSignalPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelEmail, NotificationChannelInApp}
}
func (eventManagerSignalPayload) Marshal() (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func (p eventSignalPayload) NotificationType() NotificationType { return p.Type }
func (eventSignalPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelEmail, NotificationChannelInApp}
}
func (eventSignalPayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

func init() {
	Register(eventManagerSignalPayload{})
	Register(eventLabFailedSignalPayload{})
	for _, typ := range eventScopedSignals {
		Register(eventSignalPayload{Type: NotificationType(typ)})
	}
	// Invitation emails contain a recipient-specific setup or join link. Other
	// participant signals do not guarantee this variable.
	invitationType := NotificationType(signalModel.TypeParticipantInvitationSent)
	descriptorsByType[invitationType] = append(descriptorsByType[invitationType], VariableDescriptor{
		Name: "invite_url", Description: "Invitation acceptance link", Default: "https://example.org/join",
	})
	// Event-wide notices link to the Event site and carry its times.
	for _, typ := range []signalModel.Type{signalModel.TypeParticipantEventStartReminder, signalModel.TypeParticipantEventFinished} {
		noticeType := NotificationType(typ)
		descriptorsByType[noticeType] = append(descriptorsByType[noticeType],
			VariableDescriptor{Name: "start_at", Description: "Event start (Kyiv time)", Default: "01.10.2026 10:00"},
		)
	}
	reminderType := NotificationType(signalModel.TypeParticipantEventStartReminder)
	descriptorsByType[reminderType] = append(descriptorsByType[reminderType], VariableDescriptor{
		Name: "hours_left", Description: "Hours before the start", Default: "24",
	})
	finishedType := NotificationType(signalModel.TypeParticipantEventFinished)
	descriptorsByType[finishedType] = append(descriptorsByType[finishedType], VariableDescriptor{
		Name: "finish_at", Description: "Event finish (Kyiv time)", Default: "01.10.2026 18:00",
	})
	teamInvitationType := NotificationType(signalModel.TypeParticipantTeamInvitationSent)
	descriptorsByType[teamInvitationType] = append(descriptorsByType[teamInvitationType],
		VariableDescriptor{Name: "invite_url", Description: "Team invitation acceptance link", Default: "https://example.org/join"},
		VariableDescriptor{Name: "team_name", Description: "Invited team name", Default: "Blue Team"},
		VariableDescriptor{Name: "team_url", Description: "Event team page link", Default: "https://ctf-2026.example.org/participation?tab=team"},
	)
}
