package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// BroadcastPayload is the variable contract of a custom broadcast. The content
// is authored per send, so the type has no template rows and is hidden from the
// template catalog; it only gates channels and variables.
type BroadcastPayload struct {
	UserID        string `json:"user_id" var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `json:"user_email" var:"user_email" desc:"Recipient email" default:"participant@example.org"`
	UserFirstName string `json:"user_first_name" var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `json:"user_last_name" var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserName      string `json:"user_name" var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
	EventName     string `json:"event_name" var:"event_name" desc:"Event name" default:"Cyber ICE Box CTF"`
	EventTag      string `json:"event_tag" var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	EventURL      string `json:"event_url" var:"event_url" desc:"Event site link" default:"https://ctf-2026.example.org/"`
}

func (BroadcastPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeBroadcast
}
func (BroadcastPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{
		notificationTypes.NotificationChannelEmail,
		notificationTypes.NotificationChannelInApp,
	}
}
func (p BroadcastPayload) Marshal() (json.RawMessage, error) { return json.Marshal(p) }

func init() { notificationTypes.RegisterInternal(BroadcastPayload{}) }
