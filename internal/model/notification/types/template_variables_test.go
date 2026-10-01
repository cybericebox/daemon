package notificationTypes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateEmailBodyVariables_RejectsUnknownVariableNode(t *testing.T) {
	err := ValidateEmailBodyVariables(
		NotificationType("participant.approval_registration.approved"),
		[]byte(`[{"type":"rich_text","content":{"root":{"children":[{"type":"paragraph","children":[{"type":"variable","varName":"not_provided"}]}]}}}]`),
	)
	require.Error(t, err)
}

func TestValidateEmailBodyVariables_AllowsRegisteredVariableNodeAndDynamicLink(t *testing.T) {
	err := ValidateEmailBodyVariables(
		NotificationType("participant.approval_registration.approved"),
		[]byte(`[{"type":"button","url":"https://example.org/{{scope_event_id}}"},{"type":"rich_text","content":{"root":{"children":[{"type":"paragraph","children":[{"type":"variable","varName":"user_name"}]}]}}}]`),
	)
	require.NoError(t, err)
}

func TestManagerAssignmentTemplateVariables(t *testing.T) {
	require.NoError(t, ValidateTemplateVariables(NotificationType("event.manager.assigned"), NotificationChannelInApp,
		"Вас призначено {{.role_name}} заходу {{.event_name}}"))
}

func TestParticipantInvitationTemplateAllowsAcceptanceLink(t *testing.T) {
	require.NoError(t, ValidateEmailBodyVariables(NotificationType("participant.invitation.sent"),
		[]byte(`[{"type":"button","url":"{{invite_url}}"}]`)))
	require.Error(t, ValidateEmailBodyVariables(NotificationType("participant.approval_registration.approved"),
		[]byte(`[{"type":"button","url":"{{invite_url}}"}]`)))
}

func TestTeamInvitationTemplateAllowsTeamAndLink(t *testing.T) {
	require.NoError(t, ValidateEmailBodyVariables(NotificationType("participant.team_invitation.sent"),
		[]byte(`[{"type":"button","url":"{{invite_url}}"},{"type":"rich_text","content":{"root":{"children":[{"type":"paragraph","children":[{"type":"variable","varName":"team_name"}]}]}}}]`)))
}
