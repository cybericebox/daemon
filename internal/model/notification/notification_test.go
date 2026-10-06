package notificationModel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

func TestSupports_EmailConfirmation_EmailOnly(t *testing.T) {
	assert.True(
		t,
		notificationTypes.Supports(
			notificationTypes.NotificationTypeEmailConfirmation,
			notificationTypes.NotificationChannelEmail,
		),
	)
	assert.False(
		t,
		notificationTypes.Supports(
			notificationTypes.NotificationTypeEmailConfirmation,
			notificationTypes.NotificationChannelInApp,
		),
	)
}

func TestSupports_UnknownType_False(t *testing.T) {
	assert.False(t, notificationTypes.Supports("nope", notificationTypes.NotificationChannelEmail))
}

func TestDescriptors_KnownType_CarriesTags(t *testing.T) {
	ds := notificationTypes.Descriptors(notificationTypes.NotificationTypeEmailConfirmation)
	assert.NotEmpty(t, ds)
	assert.Equal(t, "ConfirmURL", ds[0].Name)
	assert.Equal(t, "Confirmation link", ds[0].Description)
	assert.NotEmpty(t, ds[0].Default)
}

func TestDescriptors_UnknownType_Nil(t *testing.T) {
	assert.Nil(t, notificationTypes.Descriptors("nope"))
}

func TestEventSignalTypesExposeTheirVariableContractWithoutPlannerBootstrap(t *testing.T) {
	typ := notificationTypes.NotificationType("participant.approval_registration.approved")
	assert.True(t, notificationTypes.Supports(typ, notificationTypes.NotificationChannelEmail))
	assert.True(t, notificationTypes.Supports(typ, notificationTypes.NotificationChannelInApp))
	assert.NotEmpty(t, notificationTypes.Descriptors(typ))
	assert.NoError(t, notificationTypes.ValidateTemplateVariables(typ, notificationTypes.NotificationChannelEmail, "Hi {{.event_name}}, {{user_name}}"))
	assert.Error(t, notificationTypes.ValidateTemplateVariables(typ, notificationTypes.NotificationChannelEmail, "{{not_provided}}"))
}

func TestTypes_IncludesRegistered(t *testing.T) {
	all := notificationTypes.Types()
	found := false
	for _, ti := range all {
		if ti.Type == notificationTypes.NotificationTypeFlagAccepted {
			found = true
			assert.Contains(t, ti.Channels, notificationTypes.NotificationChannelInApp)
		}
	}
	assert.True(t, found, "flag_accepted must be present in Types()")
}

// Marshal is the per-dispatch vars path (replaced the reflection helper). The
// JSON keys must match the descriptor names so templates bind correctly.
func TestMarshal_ProducesVarsJSON(t *testing.T) {
	raw, err := notificationPayloads.EmailConfirmationPayload{
		ConfirmURL: "u",
		Name:       "Bob",
	}.Marshal()
	assert.NoError(t, err)
	assert.JSONEq(t, `{"ConfirmURL":"u","Name":"Bob"}`, string(raw))
}
