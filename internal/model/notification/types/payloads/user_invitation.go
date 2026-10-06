package notificationPayloads

import (
	"encoding/json"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

type UserInvitationPayload struct {
	InviteURL string `var:"InviteURL" desc:"Invitation setup link" default:"https://example.org/setup/?token=abc"`
}

func (UserInvitationPayload) NotificationType() notificationTypes.NotificationType {
	return notificationTypes.NotificationTypeUserInvitation
}
func (UserInvitationPayload) NotificationChannels() []notificationTypes.NotificationChannel {
	return []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}
}
func (p UserInvitationPayload) Marshal() (json.RawMessage, error) { return json.Marshal(p) }

func init() { notificationTypes.Register(UserInvitationPayload{}) }
