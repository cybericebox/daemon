package signalUseCase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// NotificationDispatcher is deliberately the narrow planner-facing dispatch
// port. Recipient selection and payload assembly happen here; dispatch itself
// remains a generic one-recipient delivery mechanism.
type NotificationDispatcher interface {
	Notify(context.Context, uuid.UUID, notificationTypes.NotificationPayload, ...dispatchModel.NotifyOption) error
}

type notificationSubscriptionQueries interface {
	ListEffectiveEnabledSignalNotificationSubscriptions(context.Context, postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams) ([]postgres.ListEffectiveEnabledSignalNotificationSubscriptionsRow, error)
	ListEventFormRecipientCandidates(context.Context, uuid.UUID) ([]postgres.ListEventFormRecipientCandidatesRow, error)
	GetUserByID(context.Context, uuid.UUID) (postgres.User, error)
	ListEventManagerUserIDs(context.Context, uuid.UUID) ([]uuid.UUID, error)
}

// managerActivitySignals are Event-wide notices whose in-app copy also goes to
// the Event's managers (Activity tab), in addition to the participant
// audience. Managers who are also recipients get one copy.
var managerActivitySignals = map[signalModel.Type]bool{
	signalModel.TypeParticipantEventStartReminder:    true,
	signalModel.TypeParticipantEventFinished:         true,
	signalModel.TypeParticipantEventResultsPublished: true,
}

// NotificationPlanner is the wildcard hook that turns an immutable signal into
// recipient-specific dispatches. It uses the same declarative audience model
// as Event forms, but keeps each channel delivery individual.
type NotificationPlanner struct {
	queries  notificationSubscriptionQueries
	payloads *signalModel.Registry
	dispatch NotificationDispatcher
}

func NewNotificationPlanner(
	queries notificationSubscriptionQueries,
	payloads *signalModel.Registry,
	dispatch NotificationDispatcher,
) *NotificationPlanner {
	return &NotificationPlanner{queries: queries, payloads: payloads, dispatch: dispatch}
}

func (p *NotificationPlanner) Name() string { return "notification-planner" }

func (p *NotificationPlanner) Handle(ctx context.Context, signal signalModel.Signal) error {
	payload, err := p.payloads.Decode(signal.Type, signal.Payload)
	if err != nil {
		return fmt.Errorf("notification planner: decode signal: %w", err)
	}
	routing := payload.Routing()
	if routing.ScopeEventID == uuid.Nil {
		return nil
	}

	subscriptions, err := p.queries.ListEffectiveEnabledSignalNotificationSubscriptions(ctx, postgres.ListEffectiveEnabledSignalNotificationSubscriptionsParams{
		ScopeEventID: routing.ScopeEventID,
		SignalType:   string(signal.Type),
	})
	if err != nil {
		return fmt.Errorf("notification planner: list subscriptions: %w", err)
	}
	if len(subscriptions) == 0 {
		return nil
	}

	channelsByRecipient := make(map[uuid.UUID][]notificationTypes.NotificationChannel)
	roleByRecipient := make(map[uuid.UUID]inboxModel.RecipientRole)
	recipientOrder := make([]uuid.UUID, 0)
	inAppEnabled := false
	for _, subscription := range subscriptions {
		var audience eventFormModel.Audience
		if err = json.Unmarshal(subscription.Audience, &audience); err != nil {
			return fmt.Errorf("notification planner: decode audience: %w", err)
		}
		if err = audience.Validate(); err != nil {
			return fmt.Errorf("notification planner: invalid audience: %w", err)
		}
		recipients, err := p.resolveRecipients(ctx, routing.ScopeEventID, audience, routing.SubjectUserID)
		if err != nil {
			return err
		}
		channel := notificationTypes.NotificationChannel(subscription.Channel)
		inAppEnabled = inAppEnabled || channel == notificationTypes.NotificationChannelInApp
		for _, recipient := range recipients {
			if _, exists := channelsByRecipient[recipient]; !exists {
				recipientOrder = append(recipientOrder, recipient)
				roleByRecipient[recipient] = audienceRole(audience)
			}
			channelsByRecipient[recipient] = append(channelsByRecipient[recipient], channel)
		}
	}
	if managerActivitySignals[signal.Type] && inAppEnabled {
		managers, err := p.queries.ListEventManagerUserIDs(ctx, routing.ScopeEventID)
		if err != nil {
			return fmt.Errorf("notification planner: list event managers: %w", err)
		}
		for _, manager := range managers {
			if _, exists := channelsByRecipient[manager]; exists {
				continue
			}
			recipientOrder = append(recipientOrder, manager)
			roleByRecipient[manager] = inboxModel.RoleManager
			channelsByRecipient[manager] = []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelInApp}
		}
	}
	subjectRef := inboxSubjectRef(payload)

	for _, recipient := range recipientOrder {
		channels := channelsByRecipient[recipient]
		user, profileErr := p.queries.GetUserByID(ctx, recipient)
		if profileErr != nil {
			return fmt.Errorf("notification planner: load recipient profile: %w", profileErr)
		}
		ready, available, fillErr := notificationTypes.FillSignalPayload(notificationTypes.NotificationType(signal.Type), signal.Payload, userRepo.ToDomain(user).NotificationProfile())
		if fillErr != nil {
			return fmt.Errorf("notification planner: fill recipient payload: %w", fillErr)
		}
		if !available {
			continue
		}
		if err = p.dispatch.Notify(ctx, recipient, ready,
			dispatchModel.WithOverrideChannels(channels...), dispatchModel.WithEventScope(routing.ScopeEventID),
			dispatchModel.WithInbox(inboxModel.NewMeta(string(signal.Type), roleByRecipient[recipient], subjectRef).Raised(signal.OccurredAt))); err != nil {
			return err
		}
	}
	return nil
}

// audienceRole is the inbox role of an audience's recipients: the signal
// subject is addressed personally, every other audience is Event-wide.
func audienceRole(audience eventFormModel.Audience) inboxModel.RecipientRole {
	if audience.Kind == eventFormModel.AudienceSignalSubject {
		return inboxModel.RoleSubject
	}
	return inboxModel.RoleParticipant
}

// inboxSubjectRef ties the copies of one request together; only signals that
// become requests carry one.
func inboxSubjectRef(payload signalModel.Payload) string {
	if failed, ok := payload.(*signalModel.EventLabFailedPayload); ok {
		return inboxModel.StandRef(failed.ScopeEventID, failed.TeamID)
	}
	return ""
}

func (p *NotificationPlanner) resolveRecipients(ctx context.Context, eventID uuid.UUID, audience eventFormModel.Audience, subject *uuid.UUID) ([]uuid.UUID, error) {
	if audience.Kind == eventFormModel.AudienceSignalSubject {
		if subject == nil || *subject == uuid.Nil {
			return nil, nil
		}
		return []uuid.UUID{*subject}, nil
	}
	candidates, err := p.queries.ListEventFormRecipientCandidates(ctx, eventID)
	if err != nil {
		return nil, fmt.Errorf("notification planner: list audience recipients: %w", err)
	}
	users := make([]uuid.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		if notificationAudienceMatches(candidate, audience) {
			users = append(users, candidate.UserID)
		}
	}
	return users, nil
}

func notificationAudienceMatches(candidate postgres.ListEventFormRecipientCandidatesRow, audience eventFormModel.Audience) bool {
	switch audience.Kind {
	case eventFormModel.AudienceAllParticipants:
		return true
	case eventFormModel.AudienceAllCaptains:
		return candidate.TeamRole.Valid && candidate.TeamRole.Int16 == 0
	case eventFormModel.AudienceSelectedUsers:
		return notificationAudienceContains(audience.UserIDs, candidate.UserID)
	case eventFormModel.AudienceSelectedTeams:
		return candidate.TeamID.Valid && notificationAudienceContains(audience.TeamIDs, candidate.TeamID.UUID)
	case eventFormModel.AudienceParticipantsNoTeam:
		return !candidate.TeamID.Valid
	case eventFormModel.AudienceTeamsBelowSize:
		return candidate.TeamID.Valid && audience.BelowSize != nil && candidate.TeamMemberCount < *audience.BelowSize
	default:
		return false
	}
}

func notificationAudienceContains(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// eventSiteDomain is the domain event sites live under (<tag>.<domain>); set
// at bootstrap. Every participant mail can then link to the event site.
var eventSiteDomain string

// SetEventSiteDomain sets the domain event_url is built on.
func SetEventSiteDomain(domain string) { eventSiteDomain = domain }

// participant.enrolled has no filler on purpose: the action signal published
// with it already notifies the participant, so enrolled never dispatches.
func init() {
	for _, typ := range []signalModel.Type{
		signalModel.TypeEventManagerAssigned,
		signalModel.TypeEventLabFailed,
		signalModel.TypeParticipantApprovalRegistrationSubmitted,
		signalModel.TypeParticipantApprovalRegistrationApproved,
		signalModel.TypeParticipantApprovalRegistrationRejected,
		signalModel.TypeParticipantOpenRegistrationCompleted,
		signalModel.TypeParticipantInvitationSent,
		signalModel.TypeParticipantTeamInvitationSent,
		signalModel.TypeParticipantInvitationAccepted,
		signalModel.TypeParticipantInvitationDeclined,
		signalModel.TypeParticipantInvitationRevoked,
		signalModel.TypeParticipantInvitationExpired,
		signalModel.TypeParticipantEventStartReminder,
		signalModel.TypeParticipantEventFinished,
		signalModel.TypeParticipantEventResultsPublished,
	} {
		notificationTypes.RegisterSignalPayloadFiller(notificationTypes.NotificationType(typ), func(raw json.RawMessage, recipient userModel.NotificationProfile) (notificationTypes.NotificationPayload, error) {
			vars := map[string]any{}
			if err := json.Unmarshal(raw, &vars); err != nil {
				return nil, err
			}
			// event_url is always present (empty when the site is unknown), so a
			// template that links to it never fails on a missing key.
			if url, _ := vars["event_url"].(string); url == "" {
				vars["event_url"] = ""
				if tag, _ := vars["event_tag"].(string); tag != "" && eventSiteDomain != "" {
					vars["event_url"] = "https://" + tag + "." + eventSiteDomain + "/"
				}
			}
			vars["user_id"] = recipient.ID.String()
			vars["user_email"] = recipient.Email
			vars["user_first_name"] = recipient.FirstName
			vars["user_last_name"] = recipient.LastName
			vars["user_picture"] = recipient.Picture
			vars["user_name"] = strings.TrimSpace(recipient.FirstName + " " + recipient.LastName)
			return notificationPayloads.DefaultPayload{
				Type:      notificationTypes.NotificationType(typ),
				Channels:  []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail, notificationTypes.NotificationChannelInApp},
				Variables: vars,
			}, nil
		})
	}
}
