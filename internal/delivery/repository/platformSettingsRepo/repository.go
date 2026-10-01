// Package platformSettingsRepo is the repository for the PlatformSetting
// aggregate: it accepts and returns whole domain entities and keeps all sqlc
// row mapping out of the business layer. Upsert is a single atomic statement
// (key is UNIQUE), so no unit of work is needed.
package platformSettingsRepo

import (
	"context"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	GetPlatformSettingByKey(ctx context.Context, key string) (postgres.AppSetting, error)
	ListPlatformSettings(ctx context.Context) ([]postgres.AppSetting, error)
	UpsertPlatformSetting(ctx context.Context, arg postgres.UpsertPlatformSettingParams) (postgres.AppSetting, error)
}

type Repository struct {
	q Queries
}

func New(q Queries) *Repository {
	return &Repository{q: q}
}

// GetByKey loads one setting. Not found propagates the raw repo error for the
// caller to classify (repositoryTools.IsObjectNotFoundError).
func (r *Repository) GetByKey(ctx context.Context, key string) (platformSettingsModel.PlatformSetting, error) {
	row, err := r.q.GetPlatformSettingByKey(ctx, key)
	if err != nil {
		return platformSettingsModel.PlatformSetting{}, err
	}
	return toDomain(row), nil
}

// List returns every setting ordered by key.
func (r *Repository) List(ctx context.Context) ([]platformSettingsModel.PlatformSetting, error) {
	rows, err := r.q.ListPlatformSettings(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]platformSettingsModel.PlatformSetting, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, nil
}

// Upsert writes the whole entity atomically: created on first key use,
// value/permission/updated_at replaced afterwards.
func (r *Repository) Upsert(ctx context.Context, s platformSettingsModel.PlatformSetting) (platformSettingsModel.PlatformSetting, error) {
	row, err := r.q.UpsertPlatformSetting(ctx, postgres.UpsertPlatformSettingParams{
		ID:                 s.ID,
		Key:                s.Key,
		Value:              []byte(s.Value),
		RequiredPermission: s.RequiredPermission,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	})
	if err != nil {
		return platformSettingsModel.PlatformSetting{}, err
	}
	return toDomain(row), nil
}

func toDomain(row postgres.AppSetting) platformSettingsModel.PlatformSetting {
	return platformSettingsModel.PlatformSetting{
		ID:                 row.ID,
		Key:                row.Key,
		Value:              row.Value,
		RequiredPermission: row.RequiredPermission,
		CreatedAt:          row.CreatedAt,
		UpdatedAt:          row.UpdatedAt,
	}
}
