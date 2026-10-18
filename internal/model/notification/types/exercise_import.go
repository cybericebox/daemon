package notificationTypes

import (
	"encoding/json"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// exerciseImportPayload is the end of a background task import, told to the user who started it.
type exerciseImportPayload struct {
	Type          NotificationType
	Count         string `var:"count" desc:"How many tasks were created (done only)" default:"12"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"admin@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (p exerciseImportPayload) NotificationType() NotificationType { return p.Type }
func (exerciseImportPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (exerciseImportPayload) Marshal() (json.RawMessage, error) { return json.RawMessage(`{}`), nil }

func init() {
	for _, typ := range []NotificationType{inboxModel.TypeExerciseImportDone, inboxModel.TypeExerciseImportFailed} {
		Register(exerciseImportPayload{Type: typ})
	}
}
