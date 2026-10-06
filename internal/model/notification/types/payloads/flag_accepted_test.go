package notificationPayloads_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

func TestFlagAccepted_Registered_SupportsInAppAndEmail(t *testing.T) {
	assert.True(
		t,
		notificationTypes.Supports(
			notificationTypes.NotificationTypeFlagAccepted,
			notificationTypes.NotificationChannelInApp,
		),
	)
	assert.True(
		t,
		notificationTypes.Supports(
			notificationTypes.NotificationTypeFlagAccepted,
			notificationTypes.NotificationChannelEmail,
		),
	)
}
