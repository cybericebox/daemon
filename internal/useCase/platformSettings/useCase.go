package platformSettingsUseCase

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformSettingsRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	PlatformSettingsUseCase struct {
		settings *platformSettingsRepo.Repository
	}
	Dependencies struct {
		Repo platformSettingsRepo.Queries
	}
)

func NewPlatformSettingsUseCase(deps Dependencies) *PlatformSettingsUseCase {
	return &PlatformSettingsUseCase{
		settings: platformSettingsRepo.New(deps.Repo),
	}
}

// ListPlatformSettings returns only settings the caller (from context) may read.
// A setting with an empty required_permission is public (readable by anyone).
// A setting with a non-empty required_permission is returned only when the
// caller holds that permission.
func (u *PlatformSettingsUseCase) ListPlatformSettings(
	ctx context.Context,
) ([]platformSettingsModel.PlatformSetting, error) {
	settings, err := u.settings.List(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list settings").Err()
	}
	out := make([]platformSettingsModel.PlatformSetting, 0, len(settings))
	for _, s := range settings {
		if s.RequiredPermission == "" || rbac.HasPermissionInContext(ctx, rbac.Permission(s.RequiredPermission)) {
			out = append(out, s)
		}
	}
	return out, nil
}

// GetPlatformSetting loads a setting by key and enforces the read-access permission. The
// caller's role is derived from the request context, never passed in. A caller
// who may not read it gets ErrSettingNotFound (never leaks existence).
func (u *PlatformSettingsUseCase) GetPlatformSetting(
	ctx context.Context,
	key string,
) (*platformSettingsModel.PlatformSetting, error) {
	setting, err := u.settings.GetByKey(ctx, key)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, platformSettingsModel.ErrSettingNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get setting").Err()
	}
	if setting.RequiredPermission != "" && !rbac.HasPermissionInContext(ctx, rbac.Permission(setting.RequiredPermission)) {
		return nil, platformSettingsModel.ErrSettingNotFound.Err()
	}
	return &setting, nil
}

func (u *PlatformSettingsUseCase) GetPlatformSettingValue(
	ctx context.Context,
	key string,
) (json.RawMessage, error) {
	setting, err := u.GetPlatformSetting(ctx, key)
	if err != nil {
		return nil, err
	}
	return setting.Value, nil
}

// UpsertPlatformSetting is one atomic statement now (key is UNIQUE): the old
// read→create|update transaction and its unit of work are gone.
func (u *PlatformSettingsUseCase) UpsertPlatformSetting(
	ctx context.Context,
	in UpsertInput,
) (*platformSettingsModel.PlatformSetting, error) {
	saved, err := u.settings.Upsert(
		ctx,
		platformSettingsModel.NewPlatformSetting(in.Key, in.Value, in.RequiredPermission, time.Now()),
	)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to upsert setting").Err()
	}
	return &saved, nil
}
