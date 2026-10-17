package platformSettingsUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// The test run leases without config: the defaults of EXERCISE_TEST_DEPLOY_TTL and EXERCISE_TEST_DEPLOY_TTL_MAX.
const (
	defaultTestRunLease    = 2 * time.Hour
	defaultTestRunLeaseMax = 8 * time.Hour
)

// Typed settings: retention periods, archive limits and test run leases. Each is one stored row of integer fields
// laid over its defaults; no row means the defaults. The same resolution serves the admin view and the other use
// cases, so what the form shows is what is in force.

type (
	// FieldView is one field of a typed setting for the admin form: the value in force, its default and bounds.
	FieldView struct {
		Key             string
		Kind            string
		Value           int64
		Default         int64
		Min             int64
		Max             int64
		InPrivacyPolicy bool
	}

	// TypedView is a typed setting for the admin form. Custom: a value is saved (reset brings back the defaults).
	// Ignored: a value is saved but does not apply (it does not parse or no longer validates), the defaults are in
	// force.
	TypedView struct {
		Custom    bool
		Ignored   bool
		UpdatedAt *time.Time
		Fields    []FieldView
	}

	// typedValue is a domain value a typed setting holds: it gives its fields and takes validated ones.
	typedValue[T any] interface {
		Values() platformSettingsModel.Values
		WithValues(platformSettingsModel.Values) (T, error)
	}

	typedSetting[T typedValue[T]] struct {
		key      string
		specs    []platformSettingsModel.FieldSpec
		defaults T
		// maxes are bounds tighter than the specs' that the platform sets (shown as the field's Max); check refuses
		// a value over them on save. Both optional.
		maxes map[string]int64
		check func(T) error
	}
)

func (u *PlatformSettingsUseCase) retentionSetting() typedSetting[retentionModel.Policy] {
	return typedSetting[retentionModel.Policy]{key: platformSettingsModel.RetentionKey, specs: retentionModel.SettingsFields, defaults: u.retentionDefaults}
}

func (u *PlatformSettingsUseCase) archiveLimitSetting() typedSetting[exerciseModel.ArchiveLimits] {
	s := typedSetting[exerciseModel.ArchiveLimits]{key: platformSettingsModel.ArchiveLimitsKey, specs: exerciseModel.ArchiveLimitFields, defaults: exerciseModel.DefaultArchiveLimits()}
	// One file of an archive becomes one stored file: it may not be larger than the media upload limit.
	if limit := u.maxUploadBytes; limit > 0 && limit < specMax(s.specs, "EntryBytes") {
		s.maxes = map[string]int64{"EntryBytes": limit}
		s.check = func(l exerciseModel.ArchiveLimits) error {
			if l.EntryBytes > limit {
				return platformSettingsModel.ErrArchiveEntryOverMediaLimit.WithPublicContext("field", "EntryBytes").WithPublicContext("max", limit).Err()
			}
			return nil
		}
	}
	return s
}

func (u *PlatformSettingsUseCase) testRunSetting() typedSetting[exerciseModel.TestRunLeases] {
	return typedSetting[exerciseModel.TestRunLeases]{key: platformSettingsModel.TestRunsKey, specs: exerciseModel.TestRunFields, defaults: u.testRunDefaults}
}

// specMax is the spec's own upper bound of the field key.
func specMax(specs []platformSettingsModel.FieldSpec, key string) int64 {
	for _, spec := range specs {
		if spec.Key == key {
			return spec.Max
		}
	}
	return 0
}

func (u *PlatformSettingsUseCase) GetRetentionSettings(ctx context.Context) (TypedView, error) {
	return getTyped(ctx, u, u.retentionSetting())
}

func (u *PlatformSettingsUseCase) SaveRetentionSettings(ctx context.Context, in platformSettingsModel.Values) (TypedView, error) {
	return saveTyped(ctx, u, u.retentionSetting(), in)
}

func (u *PlatformSettingsUseCase) ResetRetentionSettings(ctx context.Context) (TypedView, error) {
	return resetTyped(ctx, u, u.retentionSetting())
}

func (u *PlatformSettingsUseCase) GetArchiveLimitSettings(ctx context.Context) (TypedView, error) {
	return getTyped(ctx, u, u.archiveLimitSetting())
}

func (u *PlatformSettingsUseCase) SaveArchiveLimitSettings(ctx context.Context, in platformSettingsModel.Values) (TypedView, error) {
	return saveTyped(ctx, u, u.archiveLimitSetting(), in)
}

func (u *PlatformSettingsUseCase) ResetArchiveLimitSettings(ctx context.Context) (TypedView, error) {
	return resetTyped(ctx, u, u.archiveLimitSetting())
}

func (u *PlatformSettingsUseCase) GetTestRunSettings(ctx context.Context) (TypedView, error) {
	return getTyped(ctx, u, u.testRunSetting())
}

func (u *PlatformSettingsUseCase) SaveTestRunSettings(ctx context.Context, in platformSettingsModel.Values) (TypedView, error) {
	return saveTyped(ctx, u, u.testRunSetting(), in)
}

func (u *PlatformSettingsUseCase) ResetTestRunSettings(ctx context.Context) (TypedView, error) {
	return resetTyped(ctx, u, u.testRunSetting())
}

// RetentionPolicy is the retention policy in force: the saved periods over the defaults, BatchSize from the defaults.
func (u *PlatformSettingsUseCase) RetentionPolicy(ctx context.Context) (retentionModel.Policy, error) {
	p, _, _, err := loadTyped(ctx, u, u.retentionSetting())
	return p, err
}

// ArchiveLimits are the task archive limits in force.
func (u *PlatformSettingsUseCase) ArchiveLimits(ctx context.Context) (exerciseModel.ArchiveLimits, error) {
	l, _, _, err := loadTyped(ctx, u, u.archiveLimitSetting())
	return l, err
}

// TestRunLeases are the test deployment leases in force.
func (u *PlatformSettingsUseCase) TestRunLeases(ctx context.Context) (exerciseModel.TestRunLeases, error) {
	l, _, _, err := loadTyped(ctx, u, u.testRunSetting())
	return l, err
}

// loadTyped resolves the value in force and the stored row (nil: none). A stored value that does not parse or no
// longer validates (bounds tightened, a hand-edited row, a 0 that would purge everything) never takes effect: it is
// logged and the defaults apply (ignored). A database error is returned.
func loadTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T]) (value T, stored *platformSettingsModel.PlatformSetting, ignored bool, err error) {
	row, err := u.settings.GetByKey(ctx, s.key)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return s.defaults, nil, false, nil
		}
		var zero T
		return zero, nil, false, model.ErrPlatform.WithError(err).WithMessage("Failed to get a typed setting").Err()
	}
	var values platformSettingsModel.Values
	if err = json.Unmarshal(row.Value, &values); err != nil {
		u.warnStored(row, log.Warn().Err(err).Str("key", s.key), "Stored setting does not parse, the defaults apply")
		return s.defaults, &row, true, nil
	}
	if values == nil {
		u.warnStored(row, log.Warn().Str("key", s.key), "Stored setting is null, the defaults apply")
		return s.defaults, &row, true, nil
	}
	unknown := unknownKeys(values, s.specs)
	value, err = s.defaults.WithValues(platformSettingsModel.Overlay(s.defaults.Values(), values, s.specs))
	if err != nil {
		event := log.Warn().Err(err).Str("key", s.key)
		if field, ok := failingField(err); ok {
			event = event.Str("field", field)
			if _, fromRow := values[field]; !fromRow {
				u.warnStored(row, event, "A default of this setting is out of range (see the startup warning), the defaults apply")
				return s.defaults, &row, true, nil
			}
		}
		u.warnStored(row, event, "Stored setting is not valid, the defaults apply")
		return s.defaults, &row, true, nil
	}
	if len(unknown) > 0 {
		u.warnStored(row, log.Warn().Str("key", s.key).Strs("unknown", unknown), "Stored setting has fields no longer known, they are ignored")
	}
	return value, &row, false, nil
}

// warnStored logs a warning about a stored row once per row version: the sources are read on every pass and request,
// and the same row would repeat the same warning each time. A saved or reset row (another UpdatedAt) warns again.
func (u *PlatformSettingsUseCase) warnStored(row platformSettingsModel.PlatformSetting, event *zerolog.Event, msg string) {
	version := row.UpdatedAt.UTC().Format(time.RFC3339Nano) + "\x00" + string(row.Value)
	if previous, seen := u.warnedRows.Swap(row.Key, version); seen && previous == version {
		event.Discard()
		return
	}
	event.Msg(msg)
}

// failingField is the field a domain error names in its public context.
func failingField(err error) (string, bool) {
	var typed appErr.Error
	if !errors.As(err, &typed) {
		return "", false
	}
	field, ok := typed.PublicContext()["field"].(string)
	return field, ok
}

// unknownKeys are the keys of stored outside specs, sorted.
func unknownKeys(stored platformSettingsModel.Values, specs []platformSettingsModel.FieldSpec) []string {
	var unknown []string
	for _, key := range slices.Sorted(maps.Keys(stored)) {
		if !slices.ContainsFunc(specs, func(spec platformSettingsModel.FieldSpec) bool { return spec.Key == key }) {
			unknown = append(unknown, key)
		}
	}
	return unknown
}

// warnDefaultsOutOfRange logs one warning when the defaults of s fail its own field rules (bounds or a cross-field
// rule): an env value the form would refuse. It names the field and the env value; the defaults still apply.
func warnDefaultsOutOfRange[T typedValue[T]](s typedSetting[T]) {
	values := s.defaults.Values()
	_, err := s.defaults.WithValues(values)
	if err == nil {
		return
	}
	event := log.Warn().Err(err).Str("key", s.key)
	if field, ok := failingField(err); ok {
		event = event.Str("field", field).Int64("value", values[field])
	}
	event.Msg("An env default of this setting is outside the bounds the platform panel allows; it still applies")
}

func getTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T]) (TypedView, error) {
	value, row, ignored, err := loadTyped(ctx, u, s)
	if err != nil {
		return TypedView{}, err
	}
	return typedView(s, value, row, ignored), nil
}

// saveTyped stores every field of in after the domain accepts the whole set; a refused set writes nothing.
func saveTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T], in platformSettingsModel.Values) (TypedView, error) {
	value, err := s.defaults.WithValues(in)
	if err != nil {
		return TypedView{}, err
	}
	if s.check != nil {
		if err = s.check(value); err != nil {
			return TypedView{}, err
		}
	}
	data, err := json.Marshal(value.Values())
	if err != nil {
		return TypedView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to encode a typed setting").Err()
	}
	saved, err := u.settings.Upsert(ctx, platformSettingsModel.NewPlatformSetting(s.key, data, string(rbac.PermPlatformSettingsRead), u.now()))
	if err != nil {
		return TypedView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save a typed setting").Err()
	}
	return typedView(s, value, &saved, false), nil
}

// resetTyped removes the stored value: the defaults apply again.
func resetTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T]) (TypedView, error) {
	if err := u.settings.Delete(ctx, s.key); err != nil {
		return TypedView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to reset a typed setting").Err()
	}
	return typedView(s, s.defaults, nil, false), nil
}

func typedView[T typedValue[T]](s typedSetting[T], value T, row *platformSettingsModel.PlatformSetting, ignored bool) TypedView {
	values, defaults := value.Values(), s.defaults.Values()
	v := TypedView{Ignored: ignored, Fields: make([]FieldView, 0, len(s.specs))}
	if row != nil {
		updatedAt := row.UpdatedAt
		v.Custom, v.UpdatedAt = true, &updatedAt
	}
	for _, spec := range s.specs {
		maximum := spec.Max
		if tighter, ok := s.maxes[spec.Key]; ok {
			maximum = tighter
		}
		v.Fields = append(v.Fields, FieldView{
			Key: spec.Key, Kind: string(spec.Kind), Value: values[spec.Key], Default: defaults[spec.Key],
			Min: spec.Min, Max: maximum, InPrivacyPolicy: spec.InPrivacyPolicy,
		})
	}
	return v
}
