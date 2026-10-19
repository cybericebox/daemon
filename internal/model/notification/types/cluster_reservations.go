package notificationTypes

import (
	"encoding/json"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// reservationEndPayload is an event reservation that ends while the event runs, or was made smaller than what runs
// in it.
type reservationEndPayload struct {
	Type          NotificationType
	EventID       string `var:"event_id" desc:"Event identifier" default:"01900000-0000-7000-8000-000000000000"`
	EventName     string `var:"event_name" desc:"Event name" default:"Cyber ICE Box CTF"`
	EventTag      string `var:"event_tag" desc:"Event short tag" default:"ctf-2026"`
	EndsAt        string `var:"ends_at" desc:"When the labs are deleted (Kyiv time)" default:"18:05"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"admin@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (p reservationEndPayload) NotificationType() NotificationType { return p.Type }
func (reservationEndPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (reservationEndPayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

// testLabEndPayload is the ending notice and the end of an author's test laboratory.
type testLabEndPayload struct {
	Type          NotificationType
	DeployID      string `var:"deploy_id" desc:"Test laboratory identifier" default:"01900000-0000-7000-8000-00000000000a"`
	ExerciseName  string `var:"exercise_name" desc:"Task name" default:"Web basics"`
	EndsAt        string `var:"ends_at" desc:"When the laboratory is deleted (Kyiv time; ending only)" default:"18:05"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"admin@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (p testLabEndPayload) NotificationType() NotificationType { return p.Type }
func (testLabEndPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (testLabEndPayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

func init() {
	for _, typ := range []NotificationType{inboxModel.TypeReservationEnding, inboxModel.TypeReservationReduced} {
		Register(reservationEndPayload{Type: typ})
	}
	for _, typ := range []NotificationType{inboxModel.TypeTestLabEnding, inboxModel.TypeTestLabEnded} {
		Register(testLabEndPayload{Type: typ})
	}
}
