package event

import (
	"context"
	"encoding/json"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventNotificationRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

// NotificationSubscriptionView is the effective subscription of an Event.
// Source is "platform" when inherited from the platform default and "event"
// when the Event overrides it.
type NotificationSubscriptionView struct {
	SignalType string
	Channel    string
	Enabled    bool
	Audience   json.RawMessage
	// Config holds the per-signal options, e.g. days_before_start.
	Config json.RawMessage
	Source string
	// Required marks invitation emails: always sent, not switchable (M3).
	Required bool
}

type UpsertNotificationSubscriptionInput struct {
	SignalType string
	Channel    string
	Enabled    bool
	Audience   json.RawMessage
	// Config is optional; nil keeps the stored options.
	Config json.RawMessage
}

func (u *EventUseCase) ListNotificationSubscriptions(ctx context.Context, eventID uuid.UUID) ([]NotificationSubscriptionView, error) {
	if _, err := u.events.GetByID(ctx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	items, err := u.notificationDefaults.List(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event notification subscriptions").Err()
	}
	out := make([]NotificationSubscriptionView, 0, len(items))
	for _, item := range items {
		typ := notificationTypes.NotificationType(item.SignalType)
		// Platform-owned (moderator) signals are not Event settings.
		if !notificationTypes.IsEventScoped(typ) {
			continue
		}
		view := NotificationSubscriptionView{
			SignalType: item.SignalType, Channel: item.Channel, Enabled: item.Enabled, Audience: item.Audience, Config: item.Config, Source: item.Source,
		}
		if notificationTypes.IsRequiredEmail(typ) {
			// Invitations are sent directly by email; their in-app copy is
			// never delivered, so it is not a setting.
			if item.Channel != string(notificationTypes.NotificationChannelEmail) {
				continue
			}
			view.Enabled, view.Required = true, true
		}
		out = append(out, view)
	}
	return out, nil
}

func (u *EventUseCase) UpsertNotificationSubscription(ctx context.Context, eventID uuid.UUID, in UpsertNotificationSubscriptionInput) (NotificationSubscriptionView, error) {
	if _, err := u.events.GetByID(ctx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return NotificationSubscriptionView{}, eventModel.ErrEventNotFound.Err()
		}
		return NotificationSubscriptionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !signalModel.DefaultRegistry.Has(signalModel.Type(in.SignalType)) {
		return NotificationSubscriptionView{}, eventModel.ErrEventLifecycleInvalid.WithMessage("Unknown notification signal type").Err()
	}
	if !notificationTypes.IsEventScoped(notificationTypes.NotificationType(in.SignalType)) {
		return NotificationSubscriptionView{}, eventModel.ErrEventLifecycleInvalid.WithMessage("Notification type is not configurable per event").Err()
	}
	if notificationTypes.IsRequiredEmail(notificationTypes.NotificationType(in.SignalType)) {
		return NotificationSubscriptionView{}, mailModel.ErrRequiredNotification.Err()
	}
	if !notificationTypes.Supports(notificationTypes.NotificationType(in.SignalType), notificationTypes.NotificationChannel(in.Channel)) {
		return NotificationSubscriptionView{}, eventModel.ErrEventLifecycleInvalid.WithMessage("Notification channel is unavailable for this signal type").Err()
	}
	var audience eventFormModel.Audience
	if err := json.Unmarshal(in.Audience, &audience); err != nil {
		return NotificationSubscriptionView{}, eventModel.ErrEventLifecycleInvalid.WithMessage("Invalid notification audience").Err()
	}
	if err := audience.Validate(); err != nil {
		return NotificationSubscriptionView{}, eventModel.ErrEventLifecycleInvalid.WithMessage(err.Error()).Err()
	}
	config, err := normalizeSubscriptionConfig(in.SignalType, in.Config)
	if err != nil {
		return NotificationSubscriptionView{}, err
	}
	in.Config = config
	item, err := u.notificationDefaults.Upsert(ctx, eventID, eventNotificationRepo.Subscription(in))
	if err != nil {
		return NotificationSubscriptionView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event notification subscription").Err()
	}
	return NotificationSubscriptionView{
		SignalType: item.SignalType, Channel: item.Channel, Enabled: item.Enabled, Audience: item.Audience, Config: item.Config,
		Source: eventNotificationRepo.SourceEvent,
	}, nil
}

// ResetNotificationSubscription removes the Event override of one (signal,
// channel) pair so it inherits the platform default again. Resetting a pair
// that has no override is a no-op (idempotent).
func (u *EventUseCase) ResetNotificationSubscription(ctx context.Context, eventID uuid.UUID, signalType, channel string) error {
	if _, err := u.events.GetByID(ctx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if _, err := u.notificationDefaults.Delete(ctx, eventID, signalType, channel); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reset event notification subscription").Err()
	}
	return nil
}

// Start reminder lead time, in whole days.
const (
	minReminderDays = 1
	maxReminderDays = 30
)

// normalizeSubscriptionConfig validates the per-signal options. Only the
// start reminder has any (days_before_start); a missing config stays nil so
// the stored options are kept.
func normalizeSubscriptionConfig(signalType string, raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(raw, &config); err != nil {
		return nil, eventModel.ErrEventLifecycleInvalid.WithMessage("Invalid notification options").Err()
	}
	if signalType != string(signalModel.TypeParticipantEventStartReminder) {
		if len(config) > 0 {
			return nil, eventModel.ErrEventLifecycleInvalid.WithMessage("Notification has no options").Err()
		}
		return nil, nil
	}
	out := map[string]int{}
	for key, value := range config {
		var days int
		if key != "days_before_start" || json.Unmarshal(value, &days) != nil || days < minReminderDays || days > maxReminderDays {
			return nil, eventModel.ErrEventLifecycleInvalid.WithMessage("Start reminder must be 1-30 days before start").Err()
		}
		out[key] = days
	}
	return json.Marshal(out)
}
