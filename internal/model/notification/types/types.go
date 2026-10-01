package notificationTypes

import (
	"encoding/json"
)

type NotificationType string
type NotificationChannel string

const (
	NotificationChannelInApp NotificationChannel = "in_app"
	NotificationChannelEmail NotificationChannel = "email"
)

const (
	NotificationTypeEmailConfirmation    NotificationType = "email_confirmation"
	NotificationTypeFlagAccepted         NotificationType = "flag_accepted"
	NotificationTypeContinueRegistration NotificationType = "continue_registration"
	NotificationTypePasswordReset        NotificationType = "password_reset"
	NotificationTypeUserInvitation       NotificationType = "user_invitation"
	NotificationTypeAccountExists        NotificationType = "account_exists"
	// NotificationTypeAccountInactivityWarning warns an inactive account's
	// owner before the retention job deletes the account.
	NotificationTypeAccountInactivityWarning NotificationType = "account_inactivity_warning"
	// NotificationTypeBroadcast is a custom message an admin or an Event
	// manager composes and sends to a chosen audience.
	NotificationTypeBroadcast NotificationType = "broadcast"
)

type NotificationPayload interface {
	NotificationType() NotificationType
	NotificationChannels() []NotificationChannel
	Marshal() (json.RawMessage, error)
}
