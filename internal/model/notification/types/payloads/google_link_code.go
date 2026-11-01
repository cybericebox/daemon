package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// GoogleLinkCodePayload is the one-time code that confirms linking a Google account to this account (sign-in with a
// Google account whose address is the account's). It is the only mail of that flow: nothing is sent when the link is made.
type GoogleLinkCodePayload struct {
	Name    string `var:"Name"    desc:"User name"                  default:"John Doe"`
	Code    string `var:"Code"    desc:"One-time confirmation code" default:"123456"`
	Minutes string `var:"Minutes" desc:"How long the code lives"    default:"10"`
}

func (GoogleLinkCodePayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeGoogleLinkCode
}
func (GoogleLinkCodePayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}
func (p GoogleLinkCodePayload) Marshal() (json.RawMessage, error) { return json.Marshal(p) }

func init() { notificationTypes.Register(GoogleLinkCodePayload{}) }
