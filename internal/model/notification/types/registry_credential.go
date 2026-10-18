package notificationTypes

import (
	"encoding/json"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// registryCredentialPayload is the registry credential check: a platform token or an organizer secret stopped
// working, or works again. Sent by the inbox request router to the people who can fix it.
type registryCredentialPayload struct {
	Type          NotificationType
	Label         string `var:"label" desc:"What the credential is for: registry host and repository" default:"ghcr.io/acme/platform"`
	Reason        string `var:"reason" desc:"Reason code of the refusal" default:"unauthorized"`
	EventID       string `var:"event_id" desc:"Event identifier (organizer secret only)" default:"01900000-0000-7000-8000-000000000000"`
	EventName     string `var:"event_name" desc:"Event name (organizer secret only)" default:"Cyber ICE Box CTF"`
	EventTag      string `var:"event_tag" desc:"Event short tag (organizer secret only)" default:"ctf-2026"`
	UserID        string `var:"user_id" desc:"Recipient identifier" default:"01900000-0000-7000-8000-000000000003"`
	UserEmail     string `var:"user_email" desc:"Recipient email" default:"admin@example.org"`
	UserFirstName string `var:"user_first_name" desc:"Recipient first name" default:"Jane"`
	UserLastName  string `var:"user_last_name" desc:"Recipient last name" default:"Doe"`
	UserPicture   string `var:"user_picture" desc:"Recipient profile image URL" default:"https://example.org/avatar.png"`
	UserName      string `var:"user_name" desc:"Recipient full name" default:"Jane Doe"`
}

func (p registryCredentialPayload) NotificationType() NotificationType { return p.Type }
func (registryCredentialPayload) NotificationChannels() []NotificationChannel {
	return []NotificationChannel{NotificationChannelInApp}
}
func (registryCredentialPayload) Marshal() (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

func init() {
	for _, typ := range []NotificationType{
		inboxModel.TypeRegistryPushBroken, inboxModel.TypeRegistryPushRestored,
		inboxModel.TypeRegistryPullBroken, inboxModel.TypeRegistryPullRestored,
		inboxModel.TypeRegistrySecretBroken, inboxModel.TypeRegistrySecretRestored,
	} {
		Register(registryCredentialPayload{Type: typ})
	}
}
