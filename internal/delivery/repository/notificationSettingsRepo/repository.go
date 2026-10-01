// Package notificationSettingsRepo is the repository for global and per-user
// notification settings and the platform signal notification defaults that
// Events inherit: whole domain shapes in and out, sqlc rows only here. Every
// write is a single atomic upsert statement — no unit of work.
package notificationSettingsRepo

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	settingsModel "github.com/cybericebox/daemon/internal/model/notification/settings"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	ListNotificationSettings(ctx context.Context) ([]postgres.NotificationSetting, error)
	UpsertNotificationSetting(ctx context.Context, arg postgres.UpsertNotificationSettingParams) (postgres.NotificationSetting, error)
	ListUserSettings(ctx context.Context, userID uuid.UUID) ([]postgres.NotificationUserSetting, error)
	UpsertUserSetting(ctx context.Context, arg postgres.UpsertUserSettingParams) error
	ListPlatformSignalNotificationDefaults(ctx context.Context) ([]postgres.PlatformSignalNotificationDefault, error)
	UpsertPlatformSignalNotificationDefault(ctx context.Context, arg postgres.UpsertPlatformSignalNotificationDefaultParams) (postgres.PlatformSignalNotificationDefault, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

func (r *Repository) ListGlobal(ctx context.Context) ([]settingsModel.GlobalSetting, error) {
	rows, err := r.q.ListNotificationSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]settingsModel.GlobalSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, toGlobal(row))
	}
	return out, nil
}

func (r *Repository) UpsertGlobal(ctx context.Context, s settingsModel.GlobalSetting) (settingsModel.GlobalSetting, error) {
	row, err := r.q.UpsertNotificationSetting(ctx, postgres.UpsertNotificationSettingParams{
		NotificationType: s.NotificationType,
		Channel:          s.Channel,
		Enabled:          s.Enabled,
		UserCanChange:    s.UserCanChange,
		UserDefault:      s.UserDefault,
	})
	if err != nil {
		return settingsModel.GlobalSetting{}, err
	}
	return toGlobal(row), nil
}

func (r *Repository) ListByUser(ctx context.Context, userID uuid.UUID) ([]settingsModel.UserSetting, error) {
	rows, err := r.q.ListUserSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]settingsModel.UserSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, toUser(row))
	}
	return out, nil
}

func (r *Repository) UpsertUser(ctx context.Context, s settingsModel.UserSetting) error {
	return r.q.UpsertUserSetting(ctx, postgres.UpsertUserSettingParams{
		UserID:           s.UserID,
		NotificationType: s.NotificationType,
		Channel:          s.Channel,
		Enabled:          s.Enabled,
	})
}

func toGlobal(row postgres.NotificationSetting) settingsModel.GlobalSetting {
	return settingsModel.GlobalSetting{
		NotificationType: row.NotificationType,
		Channel:          row.Channel,
		Enabled:          row.Enabled,
		UserCanChange:    row.UserCanChange,
		UserDefault:      row.UserDefault,
	}
}

func toUser(row postgres.NotificationUserSetting) settingsModel.UserSetting {
	return settingsModel.UserSetting{
		UserID:           row.UserID,
		NotificationType: row.NotificationType,
		Channel:          row.Channel,
		Enabled:          row.Enabled,
	}
}

// ListSignalDefaults returns every platform signal default row (no type
// filtering: the read side forgives legacy/platform-only rows).
func (r *Repository) ListSignalDefaults(ctx context.Context) ([]settingsModel.SignalDefault, error) {
	rows, err := r.q.ListPlatformSignalNotificationDefaults(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]settingsModel.SignalDefault, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSignalDefault(row))
	}
	return out, nil
}

func (r *Repository) UpsertSignalDefault(ctx context.Context, d settingsModel.SignalDefault) (settingsModel.SignalDefault, error) {
	row, err := r.q.UpsertPlatformSignalNotificationDefault(ctx, postgres.UpsertPlatformSignalNotificationDefaultParams{
		SignalType: d.SignalType,
		Channel:    d.Channel,
		Enabled:    d.Enabled,
		Audience:   d.Audience,
	})
	if err != nil {
		return settingsModel.SignalDefault{}, err
	}
	return toSignalDefault(row), nil
}

func toSignalDefault(row postgres.PlatformSignalNotificationDefault) settingsModel.SignalDefault {
	return settingsModel.SignalDefault{
		SignalType: row.SignalType,
		Channel:    row.Channel,
		Enabled:    row.Enabled,
		Audience:   row.Audience,
	}
}
