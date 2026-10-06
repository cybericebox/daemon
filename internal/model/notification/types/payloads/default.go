package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type DefaultPayload struct {
	Variables map[string]interface{}
	Type      notificationTypes.NotificationType
	Channels  []notificationTypes.NotificationChannel
}

func (p DefaultPayload) NotificationType() notificationTypes.NotificationType {
	return p.Type
}
func (p DefaultPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return p.Channels
}

func (p DefaultPayload) Marshal() (json.RawMessage, error) {
	return json.Marshal(p.Variables)
}
