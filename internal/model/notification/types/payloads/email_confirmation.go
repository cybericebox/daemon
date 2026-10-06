package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type EmailConfirmationPayload struct {
	ConfirmURL string `var:"ConfirmURL" desc:"Confirmation link" default:"https://example.org/confirm"`
	Name       string `var:"Name"       desc:"User name"         default:"John Doe"`
}

func (EmailConfirmationPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeEmailConfirmation
}
func (EmailConfirmationPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}

func (p EmailConfirmationPayload) Marshal() (json.RawMessage, error) {
	return json.Marshal(p)
}

func init() { notificationTypes.Register(EmailConfirmationPayload{}) }
