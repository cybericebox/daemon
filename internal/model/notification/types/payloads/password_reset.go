package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type PasswordResetPayload struct {
	ResetURL string `var:"ResetURL" desc:"Password reset link" default:"https://example.org/reset-password?token=abc"`
	Name     string `var:"Name"     desc:"User name"           default:"John Doe"`
}

func (PasswordResetPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypePasswordReset
}
func (PasswordResetPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}
func (p PasswordResetPayload) Marshal() (json.RawMessage, error) {
	return json.Marshal(p)
}

func init() { notificationTypes.Register(PasswordResetPayload{}) }
