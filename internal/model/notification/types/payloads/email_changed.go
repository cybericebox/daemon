package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// EmailChangedPayload is sent to the address an account just left. It carries the new address and
// a link to reset the password, for the owner who did not make the change.
type EmailChangedPayload struct {
	Name     string `var:"Name"     desc:"User name"            default:"John Doe"`
	NewEmail string `var:"NewEmail" desc:"The new email address" default:"new@example.org"`
	ResetURL string `var:"ResetURL" desc:"Password reset link"  default:"https://example.org/forgot-password/"`
}

func (EmailChangedPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeEmailChanged
}
func (EmailChangedPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}
func (p EmailChangedPayload) Marshal() (json.RawMessage, error) { return json.Marshal(p) }

func init() { notificationTypes.Register(EmailChangedPayload{}) }
