package notificationPayloads_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

func TestEmailConfirmation_Channels(t *testing.T) {
	assert.Equal(
		t,
		[]notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail},
		notificationPayloads.EmailConfirmationPayload{}.NotificationChannels(),
	)
}

func TestUserInvitationPayload(t *testing.T) {
	p := notificationPayloads.UserInvitationPayload{
		InviteURL: "https://example.org/setup?token=abc",
	}
	assert.Equal(t, notificationTypes.NotificationTypeUserInvitation, p.NotificationType())
	assert.Equal(
		t,
		[]notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail},
		p.NotificationChannels(),
	)
	_, err := p.Marshal()
	assert.NoError(t, err)
}

func TestAccountExistsPayload(t *testing.T) {
	p := notificationPayloads.AccountExistsPayload{Name: "Jane Doe"}
	assert.Equal(t, notificationTypes.NotificationTypeAccountExists, p.NotificationType())
	assert.Equal(
		t,
		[]notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail},
		p.NotificationChannels(),
	)
	_, err := p.Marshal()
	assert.NoError(t, err)
}

func TestAccountInactivityWarningPayload(t *testing.T) {
	p := notificationPayloads.AccountInactivityWarningPayload{Name: "Jane", DeletionDate: "29.10.2026", SignInURL: "https://id.example.org/sign-in"}
	assert.Equal(t, notificationTypes.NotificationTypeAccountInactivityWarning, p.NotificationType())
	assert.Equal(
		t,
		[]notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail},
		p.NotificationChannels(),
	)
	raw, err := p.Marshal()
	assert.NoError(t, err)
	assert.JSONEq(t, `{"Name":"Jane","DeletionDate":"29.10.2026","SignInURL":"https://id.example.org/sign-in"}`, string(raw))
}
