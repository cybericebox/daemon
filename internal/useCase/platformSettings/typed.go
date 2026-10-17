package platformSettingsUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"time"

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
	TypedView struct {
		Custom    bool
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
	}
)

func (u *PlatformSettingsUseCase) retentionSetting() typedSetting[retentionModel.Policy] {
	return typedSetting[retentionModel.Policy]{platformSettingsModel.RetentionKey, retentionModel.SettingsFields, u.retentionDefaults}
}

func (u *PlatformSettingsUseCase) archiveLimitSetting() typedSetting[exerciseModel.ArchiveLimits] {
	return typedSetting[exerciseModel.ArchiveLimits]{platformSettingsModel.ArchiveLimitsKey, exerciseModel.ArchiveLimitFields, exerciseModel.DefaultArchiveLimits()}
}

func (u *PlatformSettingsUseCase) testRunSetting() typedSetting[exerciseModel.TestRunLeases] {
	return typedSetting[exerciseModel.TestRunLeases]{platformSettingsModel.TestRunsKey, exerciseModel.TestRunFields, u.testRunDefaults}
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
	p, _, err := loadTyped(ctx, u, u.retentionSetting())
	return p, err
}

// ArchiveLimits are the task archive limits in force.
func (u *PlatformSettingsUseCase) ArchiveLimits(ctx context.Context) (exerciseModel.ArchiveLimits, error) {
	l, _, err := loadTyped(ctx, u, u.archiveLimitSetting())
	return l, err
}

// TestRunLeases are the test deployment leases in force.
func (u *PlatformSettingsUseCase) TestRunLeases(ctx context.Context) (exerciseModel.TestRunLeases, error) {
	l, _, err := loadTyped(ctx, u, u.testRunSetting())
	return l, err
}

// loadTyped resolves the value in force and the stored row (nil: none). A stored value that does not parse or no
// longer validates (bounds tightened, a hand-edited row, a 0 that would purge everything) never takes effect: it is
// logged and the defaults apply. A database error is returned.
func loadTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T]) (T, *platformSettingsModel.PlatformSetting, error) {
	row, err := u.settings.GetByKey(ctx, s.key)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return s.defaults, nil, nil
		}
		var zero T
		return zero, nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get a typed setting").Err()
	}
	var stored platformSettingsModel.Values
	if err = json.Unmarshal(row.Value, &stored); err != nil {
		log.Warn().Err(err).Str("key", s.key).Msg("Stored setting does not parse, the defaults apply")
		return s.defaults, &row, nil
	}
	if stored == nil {
		log.Warn().Str("key", s.key).Msg("Stored setting is null, the defaults apply")
		return s.defaults, &row, nil
	}
	if unknown := unknownKeys(stored, s.specs); len(unknown) > 0 {
		log.Warn().Str("key", s.key).Strs("unknown", unknown).Msg("Stored setting has fields no longer known, they are ignored")
	}
	value, err := s.defaults.WithValues(platformSettingsModel.Overlay(s.defaults.Values(), stored, s.specs))
	if err != nil {
		if u.badDefaults[s.key] {
			log.Warn().Err(err).Str("key", s.key).Msg("A default of this setting is out of range (see the startup warning), the defaults apply")
		} else {
			log.Warn().Err(err).Str("key", s.key).Msg("Stored setting is not valid, the defaults apply")
		}
		return s.defaults, &row, nil
	}
	return value, &row, nil
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

// defaultsOutOfRange tells whether the defaults of s fail its own field rules (bounds or a cross-field rule): an env
// value the form would refuse. It logs one warning naming the field and the env value; the defaults still apply.
func defaultsOutOfRange[T typedValue[T]](s typedSetting[T]) bool {
	values := s.defaults.Values()
	_, err := s.defaults.WithValues(values)
	if err == nil {
		return false
	}
	event := log.Warn().Err(err).Str("key", s.key)
	var typed appErr.Error
	if errors.As(err, &typed) {
		if field, ok := typed.PublicContext()["field"].(string); ok {
			event = event.Str("field", field).Int64("value", values[field])
		}
	}
	event.Msg("An env default of this setting is outside the bounds the platform panel allows; it still applies")
	return true
}

func getTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T]) (TypedView, error) {
	value, row, err := loadTyped(ctx, u, s)
	if err != nil {
		return TypedView{}, err
	}
	return typedView(s, value, row), nil
}

// saveTyped stores every field of in after the domain accepts the whole set; a refused set writes nothing.
func saveTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T], in platformSettingsModel.Values) (TypedView, error) {
	value, err := s.defaults.WithValues(in)
	if err != nil {
		return TypedView{}, err
	}
	data, err := json.Marshal(value.Values())
	if err != nil {
		return TypedView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to encode a typed setting").Err()
	}
	saved, err := u.settings.Upsert(ctx, platformSettingsModel.NewPlatformSetting(s.key, data, string(rbac.PermPlatformSettingsRead), u.now()))
	if err != nil {
		return TypedView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save a typed setting").Err()
	}
	return typedView(s, value, &saved), nil
}

// resetTyped removes the stored value: the defaults apply again.
func resetTyped[T typedValue[T]](ctx context.Context, u *PlatformSettingsUseCase, s typedSetting[T]) (TypedView, error) {
	if err := u.settings.Delete(ctx, s.key); err != nil {
		return TypedView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to reset a typed setting").Err()
	}
	return typedView(s, s.defaults, nil), nil
}

func typedView[T typedValue[T]](s typedSetting[T], value T, row *platformSettingsModel.PlatformSetting) TypedView {
	values, defaults := value.Values(), s.defaults.Values()
	v := TypedView{Fields: make([]FieldView, 0, len(s.specs))}
	if row != nil {
		updatedAt := row.UpdatedAt
		v.Custom, v.UpdatedAt = true, &updatedAt
	}
	for _, spec := range s.specs {
		v.Fields = append(v.Fields, FieldView{
			Key: spec.Key, Kind: string(spec.Kind), Value: values[spec.Key], Default: defaults[spec.Key],
			Min: spec.Min, Max: spec.Max, InPrivacyPolicy: spec.InPrivacyPolicy,
		})
	}
	return v
}
