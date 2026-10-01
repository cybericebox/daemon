package notificationTypes

import signalModel "github.com/cybericebox/daemon/internal/model/signal"

// eventScopedSignals are the participant-facing signals an Event may list,
// preview and override. Everything else (account flows, manager assignment,
// future moderator-facing messages) is platform-only.
//
// participant.enrolled is deliberately absent: it always accompanies a
// specific action signal (open registration completed, approval, invitation
// accepted) that already notifies the participant, so it stays an internal
// signal without a notification of its own (one message per action).
// participant.invitation.declined is absent too: the invitee acted
// themselves, so there is no participant message (W7).
var eventScopedSignals = []signalModel.Type{
	signalModel.TypeParticipantApprovalRegistrationSubmitted,
	signalModel.TypeParticipantApprovalRegistrationApproved,
	signalModel.TypeParticipantApprovalRegistrationRejected,
	signalModel.TypeParticipantOpenRegistrationCompleted,
	signalModel.TypeParticipantInvitationSent,
	signalModel.TypeParticipantTeamInvitationSent,
	signalModel.TypeParticipantInvitationAccepted,
	signalModel.TypeParticipantInvitationRevoked,
	signalModel.TypeParticipantInvitationExpired,
	signalModel.TypeParticipantEventStartReminder,
	signalModel.TypeParticipantEventFinished,
	signalModel.TypeParticipantEventResultsPublished,
}

// requiredEmailSignals are sent directly on every invitation (M3): their email
// cannot be switched off and their in-app templates are never delivered.
var requiredEmailSignals = []signalModel.Type{
	signalModel.TypeParticipantInvitationSent,
	signalModel.TypeParticipantTeamInvitationSent,
}

// IsRequiredEmail reports whether t is an always-sent invitation email.
func IsRequiredEmail(t NotificationType) bool {
	for _, s := range requiredEmailSignals {
		if NotificationType(s) == t {
			return true
		}
	}
	return false
}

// EventScopedTypes returns the notification types an Event may list, preview
// and override. Everything else is platform-only.
func EventScopedTypes() []NotificationType {
	out := make([]NotificationType, 0, len(eventScopedSignals))
	for _, s := range eventScopedSignals {
		out = append(out, NotificationType(s))
	}
	return out
}

// IsEventScoped reports whether t is one of the event-scoped participant
// signals, i.e. safe for an Event to list, preview and override.
func IsEventScoped(t NotificationType) bool {
	// A broadcast composed in an Event goes out in the Event's sender and brand.
	if t == NotificationTypeBroadcast {
		return true
	}
	for _, s := range eventScopedSignals {
		if NotificationType(s) == t {
			return true
		}
	}
	return false
}
