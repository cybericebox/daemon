package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type FlagAcceptedPayload struct {
	Challenge string `var:"Challenge" desc:"Challenge name" default:"SQL Injection 101"`
	Points    int    `var:"Points"    desc:"Points awarded" default:"100"`
}

func (FlagAcceptedPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeFlagAccepted
}

func (FlagAcceptedPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{
		notificationTypes.NotificationChannelEmail,
		notificationTypes.NotificationChannelInApp,
	}
}

func (p FlagAcceptedPayload) Marshal() (json.RawMessage, error) {
	return json.Marshal(p)
}

func init() { notificationTypes.Register(FlagAcceptedPayload{}) }
