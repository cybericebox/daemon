package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type AccountInactivityWarningPayload struct {
	Name         string `var:"Name"         desc:"User name"                      default:"John Doe"`
	DeletionDate string `var:"DeletionDate" desc:"Date the account will be deleted" default:"29.10.2026"`
	SignInURL    string `var:"SignInURL"    desc:"Sign-in link"                   default:"https://id.example.org/sign-in"`
}

func (AccountInactivityWarningPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeAccountInactivityWarning
}
func (AccountInactivityWarningPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}
func (p AccountInactivityWarningPayload) Marshal() (json.RawMessage, error) { return json.Marshal(p) }

func init() { notificationTypes.Register(AccountInactivityWarningPayload{}) }
