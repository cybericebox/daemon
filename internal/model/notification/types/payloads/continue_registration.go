package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type ContinueRegistrationPayload struct {
	RegistrationURL string `var:"RegistrationURL" desc:"Registration setup link" default:"https://example.org/setup/?token=abc"`
	Name            string `var:"Name"            desc:"User name"               default:"John Doe"`
}

func (ContinueRegistrationPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeContinueRegistration
}
func (ContinueRegistrationPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}
func (p ContinueRegistrationPayload) Marshal() (json.RawMessage, error) {
	return json.Marshal(p)
}

func init() { notificationTypes.Register(ContinueRegistrationPayload{}) }
