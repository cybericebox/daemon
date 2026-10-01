package settingsUseCase

import (
	"context"
	"encoding/json"

	"github.com/cybericebox/daemon/internal/model"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	settingsModel "github.com/cybericebox/daemon/internal/model/notification/settings"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

// SignalDefault is the platform default subscription an Event inherits for one
// (Event-scoped signal, channel) pair until the Event overrides it.
type SignalDefault = settingsModel.SignalDefault

// ListSignalDefaults returns the platform defaults of the Event-scoped signal
// types only; platform-only types (e.g. manager assignment) are not editable
// here. Route gate: notifications.settings.read.
func (u *NotificationSettingsUseCase) ListSignalDefaults(ctx context.Context) ([]SignalDefault, error) {
	items, err := u.settings.ListSignalDefaults(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list platform signal notification defaults").
			Err()
	}
	out := make([]SignalDefault, 0, len(items))
	for _, item := range items {
		if notificationTypes.IsEventScoped(notificationTypes.NotificationType(item.SignalType)) {
			out = append(out, item)
		}
	}
	return out, nil
}

// UpsertSignalDefault validates like the Event subscription upsert (Event-
// scoped type, supported channel, valid audience) and saves the default.
// Route gate: notifications.settings.write.
func (u *NotificationSettingsUseCase) UpsertSignalDefault(ctx context.Context, in SignalDefault) (SignalDefault, error) {
	typ := notificationTypes.NotificationType(in.SignalType)
	if !notificationTypes.IsEventScoped(typ) {
		return SignalDefault{}, notificationModel.ErrSignalDefaultTypeNotEventScoped.Err()
	}
	if !notificationTypes.Supports(typ, notificationTypes.NotificationChannel(in.Channel)) {
		return SignalDefault{}, notificationModel.ErrSignalDefaultChannelUnsupported.Err()
	}
	if err := validateSignalAudience(in.Audience); err != nil {
		return SignalDefault{}, notificationModel.ErrSignalDefaultInvalidAudience.WithError(err).Err()
	}
	saved, err := u.settings.UpsertSignalDefault(ctx, in)
	if err != nil {
		return SignalDefault{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to upsert platform signal notification default").
			Err()
	}
	return saved, nil
}

func validateSignalAudience(raw json.RawMessage) error {
	var audience eventFormModel.Audience
	if err := json.Unmarshal(raw, &audience); err != nil {
		return err
	}
	return audience.Validate()
}
