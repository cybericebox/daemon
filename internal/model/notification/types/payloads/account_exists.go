package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type AccountExistsPayload struct {
	Name string `var:"Name" desc:"User name" default:"John Doe"`
}

func (AccountExistsPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeAccountExists
}
func (AccountExistsPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}
func (p AccountExistsPayload) Marshal() (json.RawMessage, error) { return json.Marshal(p) }

func init() { notificationTypes.Register(AccountExistsPayload{}) }
