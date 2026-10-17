package platformSettingsUseCase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
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
