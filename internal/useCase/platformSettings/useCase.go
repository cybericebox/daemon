package platformSettingsUseCase

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformSettingsRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	preview "github.com/cybericebox/daemon/internal/model/participantPreview"
	"github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
)

type (
	PlatformSettingsUseCase struct {
		settings          *platformSettingsRepo.Repository
		retentionDefaults retentionModel.Policy
		testRunDefaults   exerciseModel.TestRunLeases
		maxUploadBytes    int64
		// warnedRows is the stored row version last warned about, per key (warnStored).
		warnedRows sync.Map
		now        func() time.Time
	}
	Dependencies struct {
		Repo platformSettingsRepo.Queries
		// RetentionDefaults and TestRunDefaults are in force while no typed setting is saved (env RETENTION_*,
		// EXERCISE_TEST_DEPLOY_TTL(_MAX)); the archive limits default to the domain constants. A test run lease of 0
		// (no config) is 2h, its total 8h.
		RetentionDefaults retentionModel.Policy
		TestRunDefaults   exerciseModel.TestRunLeases
		// MaxUploadBytes is the limit of one stored file (MEDIA_MAX_UPLOAD_BYTES): one archive entry may not be larger.
		// Zero: no such bound.
		MaxUploadBytes int64
		// Now is the clock; nil: time.Now.
		Now func() time.Time
	}
)

func NewPlatformSettingsUseCase(deps Dependencies) *PlatformSettingsUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	testRuns := deps.TestRunDefaults
	if testRuns.Lease <= 0 {
		testRuns.Lease = defaultTestRunLease
	}
	if testRuns.Max <= 0 {
		testRuns.Max = defaultTestRunLeaseMax
	}
	u := &PlatformSettingsUseCase{
		settings:          platformSettingsRepo.New(deps.Repo),
		retentionDefaults: deps.RetentionDefaults,
		testRunDefaults:   testRuns,
		maxUploadBytes:    deps.MaxUploadBytes,
		now:               now,
	}
	warnDefaultsOutOfRange(u.retentionSetting())
	warnDefaultsOutOfRange(u.archiveLimitSetting())
	warnDefaultsOutOfRange(u.testRunSetting())
	return u
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
	if in.Key == preview.SettingKey {
		return nil, preview.ErrTypedSettingsRequired.Err()
	}
	if platformSettingsModel.IsTypedKey(in.Key) {
		return nil, platformSettingsModel.ErrTypedSettingNotGeneric.WithPublicContext("key", in.Key).Err()
	}
	saved, err := u.settings.Upsert(
		ctx,
		platformSettingsModel.NewPlatformSetting(in.Key, in.Value, in.RequiredPermission, u.now()),
	)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to upsert setting").Err()
	}
	return &saved, nil
}
