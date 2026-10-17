package platformSettingsUseCase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

const daySeconds = int64(24 * 60 * 60)

var typedNow = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func typedDefaults() (retentionModel.Policy, exerciseModel.TestRunLeases) {
	return retentionModel.DefaultPolicy(), exerciseModel.TestRunLeases{Lease: 2 * time.Hour, Max: 8 * time.Hour}
}

func setupTyped(t *testing.T) (*postgresMocks.MockQuerier, *PlatformSettingsUseCase) {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	retention, testRuns := typedDefaults()
	uc := NewPlatformSettingsUseCase(Dependencies{
		Repo: repo, RetentionDefaults: retention, TestRunDefaults: testRuns, Now: func() time.Time { return typedNow },
	})
	return repo, uc
}

func storedRow(key, value string) postgres.AppSetting {
	return postgres.AppSetting{Key: key, Value: []byte(value), RequiredPermission: string(rbac.PermPlatformSettingsRead), UpdatedAt: typedNow}
}

// captureWarnings routes the global logger into a buffer for the test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })
	return &buf
}

func TestGetRetentionSettings_WithoutRow_ShowsDefaultsInSpecOrder(t *testing.T) {
	repo, uc := setupTyped(t)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.RetentionKey).Return(postgres.AppSetting{}, pgx.ErrNoRows)

	got, err := uc.GetRetentionSettings(context.Background())
	require.NoError(t, err)
	assert.False(t, got.Custom)
	assert.Nil(t, got.UpdatedAt)
	require.Len(t, got.Fields, len(retentionModel.SettingsFields))
	for i, spec := range retentionModel.SettingsFields {
		f := got.Fields[i]
		assert.Equal(t, spec.Key, f.Key)
		assert.Equal(t, string(spec.Kind), f.Kind)
		assert.Equal(t, spec.Min, f.Min)
		assert.Equal(t, spec.Max, f.Max)
		assert.Equal(t, spec.InPrivacyPolicy, f.InPrivacyPolicy)
		assert.Equal(t, f.Default, f.Value)
	}
	assert.Equal(t, "PendingAccount", got.Fields[0].Key)
	assert.Equal(t, 30*daySeconds, got.Fields[0].Value)
	assert.True(t, got.Fields[0].InPrivacyPolicy)
	assert.False(t, got.Fields[2].InPrivacyPolicy, "InactivityGrace is not published")
}

func TestGetRetentionSettings_WithRow_ShowsStoredAndDefault(t *testing.T) {
	repo, uc := setupTyped(t)
	v := retentionModel.DefaultPolicy().Values()
	v["DeliveryLog"] = 10 * daySeconds
	data, _ := json.Marshal(v)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.RetentionKey).Return(storedRow(platformSettingsModel.RetentionKey, string(data)), nil)

	got, err := uc.GetRetentionSettings(context.Background())
	require.NoError(t, err)
	assert.True(t, got.Custom)
	require.NotNil(t, got.UpdatedAt)
	assert.Equal(t, typedNow, *got.UpdatedAt)
	for _, f := range got.Fields {
		if f.Key == "DeliveryLog" {
			assert.Equal(t, 10*daySeconds, f.Value)
			assert.Equal(t, 180*daySeconds, f.Default)
		}
	}
}

func TestGetTypedSettings_DBErrorIsReturned(t *testing.T) {
	repo, uc := setupTyped(t)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).Return(postgres.AppSetting{}, errors.New("db down")).Times(2)

	_, err := uc.GetArchiveLimitSettings(context.Background())
	require.Error(t, err)
	_, err = uc.ArchiveLimits(context.Background())
	require.Error(t, err, "a database error is not a reason to fall back to defaults")
}

func TestSaveRetentionSettings_WritesTheWholeValues(t *testing.T) {
	repo, uc := setupTyped(t)
	in := retentionModel.DefaultPolicy().Values()
	in["LabTelemetry"] = 30 * daySeconds
	repo.EXPECT().UpsertPlatformSetting(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertPlatformSettingParams) (postgres.AppSetting, error) {
			assert.Equal(t, platformSettingsModel.RetentionKey, arg.Key)
			assert.Equal(t, string(rbac.PermPlatformSettingsRead), arg.RequiredPermission)
			assert.Equal(t, typedNow, arg.UpdatedAt)
			var written platformSettingsModel.Values
			require.NoError(t, json.Unmarshal(arg.Value, &written))
			assert.Equal(t, in, written)
			return postgres.AppSetting{Key: arg.Key, Value: arg.Value, RequiredPermission: arg.RequiredPermission, UpdatedAt: arg.UpdatedAt}, nil
		})

	got, err := uc.SaveRetentionSettings(context.Background(), in)
	require.NoError(t, err)
	assert.True(t, got.Custom)
	for _, f := range got.Fields {
		if f.Key == "LabTelemetry" {
			assert.Equal(t, 30*daySeconds, f.Value)
		}
	}
}

func TestSaveRetentionSettings_GraceNotShorter_RefusedWithoutWrite(t *testing.T) {
	_, uc := setupTyped(t) // no UpsertPlatformSetting expected: a write fails the test
	in := retentionModel.DefaultPolicy().Values()
	in["InactiveAccount"] = 180 * daySeconds
	in["InactivityGrace"] = 180 * daySeconds

	_, err := uc.SaveRetentionSettings(context.Background(), in)
	assert.True(t, errors.Is(err, platformSettingsModel.ErrRetentionGraceNotShorter.Err()), "got %v", err)
}

func TestSaveTypedSettings_PartialOrUnknown_RefusedWithoutWrite(t *testing.T) {
	_, uc := setupTyped(t)
	_, err := uc.SaveTestRunSettings(context.Background(), platformSettingsModel.Values{"Lease": 3600})
	assert.True(t, errors.Is(err, platformSettingsModel.ErrSettingValueMissing.Err()), "got %v", err)
	_, err = uc.SaveArchiveLimitSettings(context.Background(), platformSettingsModel.Values{
		"ArchiveBytes": 1 << 20, "EntryBytes": 1 << 20, "UnpackedBytes": 1 << 20, "Entries": 10, "Extra": 1,
	})
	assert.True(t, errors.Is(err, platformSettingsModel.ErrSettingUnknownField.Err()), "got %v", err)
}

func TestResetTestRunSettings_DeletesTheKeyAndShowsDefaults(t *testing.T) {
	repo, uc := setupTyped(t)
	repo.EXPECT().DeletePlatformSettingByKey(gomock.Any(), platformSettingsModel.TestRunsKey).Return(nil)

	got, err := uc.ResetTestRunSettings(context.Background())
	require.NoError(t, err)
	assert.False(t, got.Custom)
	require.Len(t, got.Fields, 2)
	assert.Equal(t, int64(7200), got.Fields[0].Value)
	assert.Equal(t, int64(28800), got.Fields[1].Value)
}

func TestRetentionPolicy_WithoutRow_IsTheDefaults(t *testing.T) {
	repo, uc := setupTyped(t)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.RetentionKey).Return(postgres.AppSetting{}, pgx.ErrNoRows)

	got, err := uc.RetentionPolicy(context.Background())
	require.NoError(t, err)
	assert.Equal(t, retentionModel.DefaultPolicy(), got)
}

func TestRetentionPolicy_InvalidStoredValue_FallsBackToDefaultsWithWarning(t *testing.T) {
	for name, stored := range map[string]string{
		"a zero period":     `{"DeliveryLog":0}`,
		"not json":          `{"DeliveryLog":`,
		"a fraction":        `{"DeliveryLog":1.5}`,
		"grace too long":    `{"InactiveAccount":15552000,"InactivityGrace":15552000}`,
		"above the maximum": `{"UnusedAnswerFile":99999999}`,
	} {
		t.Run(name, func(t *testing.T) {
			warnings := captureWarnings(t)
			repo, uc := setupTyped(t)
			repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.RetentionKey).Return(storedRow(platformSettingsModel.RetentionKey, stored), nil)

			got, err := uc.RetentionPolicy(context.Background())
			require.NoError(t, err)
			assert.Equal(t, retentionModel.DefaultPolicy(), got)
			assert.Contains(t, warnings.String(), `"level":"warn"`)
			assert.Contains(t, warnings.String(), platformSettingsModel.RetentionKey)
		})
	}
}

func TestRetentionPolicy_ValidOverride_OthersDefaultBatchSizeKept(t *testing.T) {
	repo, uc := setupTyped(t)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.RetentionKey).
		Return(storedRow(platformSettingsModel.RetentionKey, `{"DeliveryLog":864000}`), nil)

	got, err := uc.RetentionPolicy(context.Background())
	require.NoError(t, err)
	want := retentionModel.DefaultPolicy()
	want.DeliveryLog = 10 * 24 * time.Hour
	assert.Equal(t, want, got)
	assert.Equal(t, int32(retentionModel.DefaultBatchSize), got.BatchSize)
}

func TestArchiveLimitsAndTestRunLeases_Sources(t *testing.T) {
	repo, uc := setupTyped(t)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).
		Return(storedRow(platformSettingsModel.ArchiveLimitsKey, `{"Entries":50}`), nil)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.TestRunsKey).
		Return(storedRow(platformSettingsModel.TestRunsKey, `{"Lease":3600}`), nil)

	limits, err := uc.ArchiveLimits(context.Background())
	require.NoError(t, err)
	want := exerciseModel.DefaultArchiveLimits()
	want.Entries = 50
	assert.Equal(t, want, limits)

	leases, err := uc.TestRunLeases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, exerciseModel.TestRunLeases{Lease: time.Hour, Max: 8 * time.Hour}, leases)
}

func TestGenericUpsert_TypedKeyRefused(t *testing.T) {
	_, uc := setupTyped(t) // no write expected
	for _, key := range []string{platformSettingsModel.RetentionKey, platformSettingsModel.ArchiveLimitsKey, platformSettingsModel.TestRunsKey} {
		_, err := uc.UpsertPlatformSetting(context.Background(), UpsertInput{Key: key, Value: json.RawMessage(`{"DeliveryLog":0}`)})
		assert.True(t, errors.Is(err, platformSettingsModel.ErrTypedSettingNotGeneric.Err()), "%s: got %v", key, err)
	}
}

// A use case built without the test run config (zero leases) keeps today's fallback: 2h, at most 8h.
func TestTestRunLeases_ZeroDefaultsFallBackTo2hAnd8h(t *testing.T) {
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := NewPlatformSettingsUseCase(Dependencies{Repo: repo, RetentionDefaults: retentionModel.DefaultPolicy()})
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.TestRunsKey).Return(postgres.AppSetting{}, pgx.ErrNoRows).Times(2)

	leases, err := uc.TestRunLeases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, exerciseModel.TestRunLeases{Lease: 2 * time.Hour, Max: 8 * time.Hour}, leases)

	uc = NewPlatformSettingsUseCase(Dependencies{Repo: repo, RetentionDefaults: retentionModel.DefaultPolicy(), TestRunDefaults: exerciseModel.TestRunLeases{Lease: 3 * time.Hour}})
	leases, err = uc.TestRunLeases(context.Background())
	require.NoError(t, err)
	assert.Equal(t, exerciseModel.TestRunLeases{Lease: 3 * time.Hour, Max: 8 * time.Hour}, leases, "each lease falls back on its own")
}

// An env default outside its field bounds is named once at construction; a stored row that only fails because of
// it is not blamed.
func TestEnvDefaultOutOfBounds_WarnsAtConstructionAndDoesNotBlameTheRow(t *testing.T) {
	warnings := captureWarnings(t)
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	retention := retentionModel.DefaultPolicy()
	retention.UnusedAnswerFile = time.Hour // the field's minimum is 24h
	uc := NewPlatformSettingsUseCase(Dependencies{Repo: repo, RetentionDefaults: retention, TestRunDefaults: exerciseModel.TestRunLeases{Lease: 2 * time.Hour, Max: 8 * time.Hour}})

	startup := warnings.String()
	assert.Contains(t, startup, `"level":"warn"`)
	assert.Contains(t, startup, platformSettingsModel.RetentionKey)
	assert.Contains(t, startup, `"field":"UnusedAnswerFile"`)
	assert.Contains(t, startup, `"value":3600`)
	assert.NotContains(t, startup, platformSettingsModel.TestRunsKey, "valid defaults are not reported")
	warnings.Reset()

	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.RetentionKey).
		Return(storedRow(platformSettingsModel.RetentionKey, `{"DeliveryLog":864000}`), nil)
	got, err := uc.RetentionPolicy(context.Background())
	require.NoError(t, err)
	assert.Equal(t, retention, got, "the defaults apply")
	assert.Contains(t, warnings.String(), "default")
	assert.NotContains(t, warnings.String(), "Stored setting is not valid")
}

// Cross-field rules count too: an env grace not shorter than the inactivity period.
func TestEnvDefaultCrossField_WarnsAtConstruction(t *testing.T) {
	warnings := captureWarnings(t)
	retention := retentionModel.DefaultPolicy()
	retention.InactivityGrace = retention.InactiveAccount
	NewPlatformSettingsUseCase(Dependencies{Repo: postgresMocks.NewMockQuerier(gomock.NewController(t)), RetentionDefaults: retention})

	assert.Contains(t, warnings.String(), platformSettingsModel.RetentionKey)
	assert.Contains(t, warnings.String(), `"field":"InactivityGrace"`)
}

func TestLoadTyped_NullOrForeignKeys_WarnStillSafe(t *testing.T) {
	t.Run("null", func(t *testing.T) {
		warnings := captureWarnings(t)
		repo, uc := setupTyped(t)
		repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).
			Return(storedRow(platformSettingsModel.ArchiveLimitsKey, `null`), nil)

		got, err := uc.ArchiveLimits(context.Background())
		require.NoError(t, err)
		assert.Equal(t, exerciseModel.DefaultArchiveLimits(), got)
		assert.Contains(t, warnings.String(), `"level":"warn"`)
		assert.Contains(t, warnings.String(), platformSettingsModel.ArchiveLimitsKey)
	})
	t.Run("foreign keys", func(t *testing.T) {
		warnings := captureWarnings(t)
		repo, uc := setupTyped(t)
		repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).
			Return(storedRow(platformSettingsModel.ArchiveLimitsKey, `{"Entries":50,"Retired":7,"Old":1}`), nil)

		got, err := uc.ArchiveLimits(context.Background())
		require.NoError(t, err)
		want := exerciseModel.DefaultArchiveLimits()
		want.Entries = 50
		assert.Equal(t, want, got, "the known fields still apply")
		assert.Contains(t, warnings.String(), `"level":"warn"`)
		assert.Contains(t, warnings.String(), `"unknown":["Old","Retired"]`)
	})
}

// A typed row is gated by platform.settings.read: a caller holding only the value permission (the public role)
// cannot read it through GET settings/:key/value.
func TestGetPlatformSettingValue_TypedRowHiddenFromValueOnlyCaller(t *testing.T) {
	repo, uc := setupTyped(t)
	for _, key := range []string{platformSettingsModel.RetentionKey, platformSettingsModel.ArchiveLimitsKey, platformSettingsModel.TestRunsKey} {
		repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), key).Return(storedRow(key, `{"Lease":3600}`), nil)
		ctx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{Role: rbac.RolePublic})
		require.True(t, rbac.HasPermissionInContext(ctx, rbac.PermPlatformSettingsReadValue), "the public role holds the value permission")

		value, err := uc.GetPlatformSettingValue(ctx, key)
		assert.Nil(t, value)
		assert.True(t, errors.Is(err, platformSettingsModel.ErrSettingNotFound.Err()), "%s: got %v", key, err)
	}
}

// With a bad env default, a stored row whose own field is out of range is still blamed; the default is blamed only
// when the failing field is not in the row.
func TestLoadTyped_BlamesWhoseFieldFails(t *testing.T) {
	retention := retentionModel.DefaultPolicy()
	retention.UnusedAnswerFile = time.Hour // out of range
	for name, tc := range map[string]struct {
		stored    string
		wantBlame string
	}{
		"the stored field fails":  {`{"UnusedAnswerFile":60}`, "Stored setting is not valid"},
		"the default field fails": {`{"DeliveryLog":864000}`, "A default of this setting is out of range"},
		"a stored field over max": {`{"DeliveryLog":999999999,"UnusedAnswerFile":86400}`, "Stored setting is not valid"},
	} {
		t.Run(name, func(t *testing.T) {
			warnings := captureWarnings(t)
			repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
			uc := NewPlatformSettingsUseCase(Dependencies{Repo: repo, RetentionDefaults: retention})
			warnings.Reset()
			repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.RetentionKey).
				Return(storedRow(platformSettingsModel.RetentionKey, tc.stored), nil)

			_, err := uc.RetentionPolicy(context.Background())
			require.NoError(t, err)
			assert.Contains(t, warnings.String(), tc.wantBlame)
		})
	}
}

// A stored row's warning is logged once per row content, not on every read; a changed row warns again.
func TestLoadTyped_WarnsOncePerStoredRow(t *testing.T) {
	warnings := captureWarnings(t)
	repo, uc := setupTyped(t)
	row := storedRow(platformSettingsModel.ArchiveLimitsKey, `{"Entries":50,"Old":1}`)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).Return(row, nil).Times(3)
	for range 3 {
		_, err := uc.ArchiveLimits(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, 1, strings.Count(warnings.String(), `"level":"warn"`))

	changed := storedRow(platformSettingsModel.ArchiveLimitsKey, `null`)
	changed.UpdatedAt = typedNow.Add(time.Minute)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).Return(changed, nil).Times(2)
	for range 2 {
		_, err := uc.ArchiveLimits(context.Background())
		require.NoError(t, err)
	}
	assert.Equal(t, 2, strings.Count(warnings.String(), `"level":"warn"`))
}

// One archive entry may not be larger than one stored file (MEDIA_MAX_UPLOAD_BYTES): the form's bound says so and a
// save over it is refused under EntryBytes, nothing written.
func TestArchiveLimits_EntryBoundByTheMediaUploadLimit(t *testing.T) {
	const mib = int64(1 << 20)
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	retention, testRuns := typedDefaults()
	uc := NewPlatformSettingsUseCase(Dependencies{Repo: repo, RetentionDefaults: retention, TestRunDefaults: testRuns, MaxUploadBytes: 256 * mib})
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).Return(postgres.AppSetting{}, pgx.ErrNoRows)

	view, err := uc.GetArchiveLimitSettings(context.Background())
	require.NoError(t, err)
	for _, f := range view.Fields {
		switch f.Key {
		case "EntryBytes":
			assert.Equal(t, 256*mib, f.Max, "the media upload limit is tighter than 1 GiB")
		case "UnpackedBytes", "ArchiveBytes":
			assert.Equal(t, 1024*mib, f.Max)
		}
	}

	_, err = uc.SaveArchiveLimitSettings(context.Background(), platformSettingsModel.Values{
		"ArchiveBytes": 512 * mib, "EntryBytes": 256*mib + 1, "UnpackedBytes": 512 * mib, "Entries": 1000,
	})
	require.ErrorIs(t, err, platformSettingsModel.ErrArchiveEntryOverMediaLimit.Err())
	var typed appErr.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, "EntryBytes", typed.PublicContext()["field"])
	assert.EqualValues(t, 256*mib, typed.PublicContext()["max"])

	// A media limit above 1 GiB leaves the field's own bound.
	big := NewPlatformSettingsUseCase(Dependencies{Repo: repo, RetentionDefaults: retention, TestRunDefaults: testRuns, MaxUploadBytes: 4096 * mib})
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.ArchiveLimitsKey).Return(postgres.AppSetting{}, pgx.ErrNoRows)
	view, err = big.GetArchiveLimitSettings(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1024*mib, view.Fields[1].Max)
}

// Ignored: a saved row exists (Custom) but does not apply, so the defaults are in force.
func TestTypedView_IgnoredWhenTheSavedRowDoesNotApply(t *testing.T) {
	for name, tc := range map[string]struct {
		stored  string
		ignored bool
	}{
		"valid":        {`{"Lease":3600,"LeaseMax":28800}`, false},
		"out of range": {`{"Lease":1}`, true},
		"broken":       {`{"Lease":`, true},
		"null":         {`null`, true},
	} {
		t.Run(name, func(t *testing.T) {
			repo, uc := setupTyped(t)
			repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.TestRunsKey).
				Return(storedRow(platformSettingsModel.TestRunsKey, tc.stored), nil)
			view, err := uc.GetTestRunSettings(context.Background())
			require.NoError(t, err)
			assert.True(t, view.Custom)
			assert.Equal(t, tc.ignored, view.Ignored)
		})
	}
	repo, uc := setupTyped(t)
	repo.EXPECT().GetPlatformSettingByKey(gomock.Any(), platformSettingsModel.TestRunsKey).Return(postgres.AppSetting{}, pgx.ErrNoRows)
	view, err := uc.GetTestRunSettings(context.Background())
	require.NoError(t, err)
	assert.False(t, view.Custom)
	assert.False(t, view.Ignored, "no row: nothing ignored")
}

// The EntryBytes default (512 MiB) over a lower media upload limit is reported at construction like a bound.
func TestEnvDefaultOverTheMediaLimit_WarnsAtConstruction(t *testing.T) {
	warnings := captureWarnings(t)
	retention, testRuns := typedDefaults()
	NewPlatformSettingsUseCase(Dependencies{Repo: postgresMocks.NewMockQuerier(gomock.NewController(t)), RetentionDefaults: retention, TestRunDefaults: testRuns, MaxUploadBytes: 256 << 20})

	assert.Contains(t, warnings.String(), platformSettingsModel.ArchiveLimitsKey)
	assert.Contains(t, warnings.String(), `"field":"EntryBytes"`)
	assert.Contains(t, warnings.String(), `"value":536870912`)
}

// A saved lease never exceeds what the calendar can book.
func TestTestRunLeaseMax_IsTheCalendarBookingLimit(t *testing.T) {
	for _, spec := range exerciseModel.TestRunFields {
		if spec.Key == "Lease" {
			assert.Equal(t, int64(calModel.MaxBooking/time.Second), spec.Max)
			return
		}
	}
	t.Fatal("no Lease field")
}
