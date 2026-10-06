package settingsUseCase

import (
	"context"

	"github.com/cybericebox/daemon/internal/model"
	settingsModel "github.com/cybericebox/daemon/internal/model/notification/settings"
)

func (u *NotificationSettingsUseCase) ListGlobalSettings(
	ctx context.Context,
) ([]settingsModel.GlobalSetting, error) {
	settings, err := u.settings.ListGlobal(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list global notification settings").
			Err()
	}
	return settings, nil
}

func (u *NotificationSettingsUseCase) UpsertGlobalSetting(
	ctx context.Context,
	in settingsModel.UpsertGlobalInput,
) (settingsModel.GlobalSetting, error) {
	saved, err := u.settings.UpsertGlobal(ctx, settingsModel.GlobalSetting{
		NotificationType: in.NotificationType,
		Channel:          in.Channel,
		Enabled:          in.Enabled,
		UserCanChange:    in.UserCanChange,
		UserDefault:      in.UserDefault,
	})
	if err != nil {
		return settingsModel.GlobalSetting{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to upsert global notification setting").
			Err()
	}
	return saved, nil
}
