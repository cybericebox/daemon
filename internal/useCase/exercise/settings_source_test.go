package exercise_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

// fakeSettings is the platform settings source: the archive limits and test run leases in force, or a failure.
type fakeSettings struct {
	limits       exerciseModel.ArchiveLimits
	leases       exerciseModel.TestRunLeases
	err          error
	archiveReads int
}

func (f *fakeSettings) ArchiveLimits(context.Context) (exerciseModel.ArchiveLimits, error) {
	f.archiveReads++
	return f.limits, f.err
}

func (f *fakeSettings) TestRunLeases(context.Context) (exerciseModel.TestRunLeases, error) {
	return f.leases, f.err
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })
	return &buf
}

// threeEntries is a plain archive of three files and no task document.
func threeEntries(t *testing.T) []byte {
	t.Helper()
	data, err := exercise.BuildArchiveNoManifest(map[string][]byte{"a.txt": []byte("a"), "b.txt": []byte("b"), "c.txt": []byte("c")})
	require.NoError(t, err)
	return data
}

func TestArchiveLimits_TheSavedLimitsApply(t *testing.T) {
	limits := exerciseModel.DefaultArchiveLimits()
	limits.Entries = 2
	settings := &fakeSettings{limits: limits}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Settings: settings})

	require.Equal(t, limits, uc.ArchiveLimits(context.Background()))
	_, err := uc.ImportArchiveBundle(context.Background(), threeEntries(t), "", uuid.Must(uuid.NewV7()))
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err(), "three entries over a saved cap of two")
	require.Equal(t, 2, settings.archiveReads, "one read per import")
}

func TestArchiveLimits_SourceErrorFallsBackToTheDefaults(t *testing.T) {
	warnings := captureLog(t)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Settings: &fakeSettings{err: errors.New("db down")}})

	require.Equal(t, exerciseModel.DefaultArchiveLimits(), uc.ArchiveLimits(context.Background()))
	require.Contains(t, warnings.String(), `"level":"warn"`)
	_, err := uc.ImportArchiveBundle(context.Background(), threeEntries(t), "", uuid.Must(uuid.NewV7()))
	require.Error(t, err, "no task document")
	require.NotErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err(), "the default 1000 entries apply")

	require.Equal(t, exerciseModel.DefaultArchiveLimits(), exercise.NewExerciseUseCase(exercise.Dependencies{}).ArchiveLimits(context.Background()),
		"no source: the defaults")
}

// A zero ImportExerciseInput.Limits means the limits in force.
func TestImportExerciseArchive_ZeroLimitsReadTheSource(t *testing.T) {
	limits := exerciseModel.DefaultArchiveLimits()
	limits.Entries = 2
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Settings: &fakeSettings{limits: limits}})

	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: threeEntries(t)})
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err())
}

func TestDeployVariantTest_StaticTakesTheSavedLease(t *testing.T) {
	q, variantID, versionID, ownerID := staticFixture(t, nil, nil)
	var created postgres.CreateExerciseTestDeployParams
	expectStaticCreate(q, &created)
	gate := &fakeGate{}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Settings: &fakeSettings{leases: exerciseModel.TestRunLeases{Lease: 3 * time.Hour, Max: 8 * time.Hour}}})
	uc.SetTestLabGate(gate)
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	uc.SetClock(func() time.Time { return now })

	_, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID)
	require.NoError(t, err)
	require.True(t, created.ExpiresAt.Equal(now.Add(3*time.Hour)), "expires %s", created.ExpiresAt)
	require.Len(t, gate.admitted, 1)
	require.Equal(t, 3*time.Hour, gate.admitted[0].Lease, "the calendar holds the same lease")
}

func TestDeployVariantTest_SourceErrorUsesTheConfigLease(t *testing.T) {
	warnings := captureLog(t)
	q, variantID, versionID, ownerID := staticFixture(t, nil, nil)
	var created postgres.CreateExerciseTestDeployParams
	expectStaticCreate(q, &created)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{
		Repo: q, FlagConfig: config.ExerciseConfig{TestDeployTTL: 90 * time.Minute}, Settings: &fakeSettings{err: errors.New("db down")},
	})
	now := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	uc.SetClock(func() time.Time { return now })

	_, err := uc.DeployVariantTest(context.Background(), ownerID, versionID, variantID)
	require.NoError(t, err)
	require.True(t, created.ExpiresAt.Equal(now.Add(90*time.Minute)), "expires %s", created.ExpiresAt)
	require.Contains(t, warnings.String(), `"level":"warn"`)
}

func TestExtendTestDeploy_CappedByTheSavedMaximum(t *testing.T) {
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Date(2026, 10, 18, 18, 0, 0, 0, time.UTC)
	createdAt := now.Add(-2 * time.Hour)
	row := postgres.ExerciseTestDeployment{ID: deployID, CreatedBy: userID, CreatedAt: createdAt, ExpiresAt: now.Add(time.Minute)}
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(row, nil)
	var extendedTo time.Time
	q.EXPECT().ExtendOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ExtendOwnedExerciseTestDeployParams) (postgres.ExerciseTestDeployment, error) {
			extendedTo = arg.ExpiresAt
			row.ExpiresAt = arg.ExpiresAt
			return row, nil
		})
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Settings: &fakeSettings{leases: exerciseModel.TestRunLeases{Lease: 3 * time.Hour, Max: 4 * time.Hour}}})
	uc.SetTestLabGate(&fakeGate{})
	uc.SetClock(func() time.Time { return now })

	_, err := uc.ExtendTestDeploy(context.Background(), userID, deployID)
	require.NoError(t, err)
	require.True(t, extendedTo.Equal(createdAt.Add(4*time.Hour)), "now+3h is capped at start+4h: %s", extendedTo)
}
