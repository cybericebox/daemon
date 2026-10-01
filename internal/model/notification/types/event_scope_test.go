package notificationTypes

import (
	"testing"

	"github.com/stretchr/testify/require"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

func TestEventScopedTypes(t *testing.T) {
	require.False(t, IsEventScoped(NotificationType(signalModel.TypeParticipantEnrolled)))
	require.True(t, IsEventScoped(NotificationType(signalModel.TypeParticipantInvitationExpired)))
	require.True(t, IsEventScoped(NotificationType(signalModel.TypeParticipantTeamInvitationSent)))
	require.False(t, IsEventScoped(NotificationType(signalModel.TypeEventManagerAssigned)))
	require.False(t, IsEventScoped(NotificationTypeEmailConfirmation))
	require.False(t, IsEventScoped(NotificationType(signalModel.TypeParticipantInvitationDeclined)), "the invitee declined: no participant mail")
	require.True(t, IsEventScoped(NotificationType(signalModel.TypeParticipantEventStartReminder)))
	require.True(t, IsEventScoped(NotificationType(signalModel.TypeParticipantEventFinished)))
	require.False(t, IsEventScoped(NotificationType(signalModel.TypeEventLabFailed)), "moderator mail stays platform")
	require.True(t, IsEventScoped(NotificationType(signalModel.TypeParticipantEventResultsPublished)))
	require.Len(t, EventScopedTypes(), 12)
}

func TestRequiredEmail(t *testing.T) {
	require.True(t, IsRequiredEmail(NotificationType(signalModel.TypeParticipantInvitationSent)))
	require.True(t, IsRequiredEmail(NotificationType(signalModel.TypeParticipantTeamInvitationSent)))
	require.False(t, IsRequiredEmail(NotificationType(signalModel.TypeParticipantInvitationRevoked)))
}

func TestEventNoticeVariables(t *testing.T) {
	names := map[string]bool{}
	for _, d := range Descriptors(NotificationType(signalModel.TypeParticipantEventStartReminder)) {
		names[d.Name] = true
	}
	for _, want := range []string{"event_name", "event_url", "start_at", "hours_left"} {
		require.True(t, names[want], want)
	}
	require.True(t, Supports(NotificationType(signalModel.TypeParticipantEventFinished), NotificationChannelEmail))
}
