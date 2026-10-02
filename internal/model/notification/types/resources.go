package notificationTypes

import (
	"encoding/json"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// resourceChangePayload is the resource calendar change flow: the request to the platform admins and the decision
// to the organizer.
type resourceChangePayload struct {
	Type          NotificationType
	ChangeID      string `var:"change_id" desc:"Change request identifier" default:"01900000-0000-7000-8000-000000000008"`
	EventID       string `var:"event_id" desc:"Event identifier" default:"01900000-0000-7000-8000-000000000000"`
	EventName     string `var:"event_name" desc:"Event name" default:"Cyber ICE Box CTF"`
	EventTag      string `var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	RequesterName string `var:"requester_name" desc:"Organizer full name (email when unnamed)" default:"Jane Doe"`
	Reason        string `var:"reason" desc:"Organizer reason" default:"More teams joined"`
	Summary       string `var:"summary" desc:"What the organizer asks for" default:"size 8 / 16Gi; until 2026-10-05 18:00 UTC"`
	DecisionNote  string `var:"decision_note" desc:"Admin note (decisions only)" default:""`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"admin@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (p resourceChangePayload) NotificationType() NotificationType { return p.Type }
func (resourceChangePayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (resourceChangePayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

// resourceAlarmPayload is the readiness alarm to the platform admins.
type resourceAlarmPayload struct {
	AlarmID       string `var:"alarm_id" desc:"Alarm identifier" default:"01900000-0000-7000-8000-000000000009"`
	AlarmKind     string `var:"alarm_kind" desc:"Why: not_placed, agent_lost, agent_shrunk or not_connected" default:"not_connected"`
	EventID       string `var:"event_id" desc:"Event identifier" default:"01900000-0000-7000-8000-000000000000"`
	EventName     string `var:"event_name" desc:"Event name" default:"Cyber ICE Box CTF"`
	EventTag      string `var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	AgentName     string `var:"agent_name" desc:"Laboratory agent name (empty when none)" default:"main"`
	Units         string `var:"units" desc:"Teams affected" default:"3"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"admin@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (resourceAlarmPayload) NotificationType() NotificationType {
	return inboxModel.TypeResourceAlarmRaised
}
func (resourceAlarmPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (resourceAlarmPayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

func init() {
	for _, typ := range []NotificationType{inboxModel.TypeResourceChangeRequested, inboxModel.TypeResourceChangeApproved, inboxModel.TypeResourceChangeRejected} {
		Register(resourceChangePayload{Type: typ})
	}
	Register(resourceAlarmPayload{})
}
