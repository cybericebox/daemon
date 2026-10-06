package settingsUseCase

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	settingsModel "github.com/cybericebox/daemon/internal/model/notification/settings"
)

func (u *NotificationSettingsUseCase) ListUserSettings(ctx context.Context, userID uuid.UUID) (
	[]settingsModel.UserSetting,
	error,
) {
	settings, err := u.settings.ListByUser(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list user notification settings").
			Err()
	}
	return settings, nil
}

func (u *NotificationSettingsUseCase) UpsertUserSetting(
	ctx context.Context,
	in settingsModel.UpsertUserInput,
) error {
	if err := u.settings.UpsertUser(ctx, settingsModel.UserSetting{
		UserID:           in.UserID,
		NotificationType: in.NotificationType,
		Channel:          in.Channel,
		Enabled:          in.Enabled,
	}); err != nil {
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to upsert user notification setting").
			Err()
	}
	return nil
}
