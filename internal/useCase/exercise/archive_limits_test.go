package exercise_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// exportUnder exports the fixture task (four distinct attachments of 7-20 bytes, a document of a few KiB) under the
// limits in force.
func exportUnder(t *testing.T) ([]byte, error) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	expectExport(t, q, exportExerciseID, "Експорт", exportFixtureVersions())
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: exportFixtureMedia()})
	return uc.ExportExerciseArchive(context.Background(), exportExerciseID, exercise.ExportOptions{})
}

// The export refuses what the import would refuse: one set of limits for both.
func TestExportExerciseArchive_EntryOverTheEntryCap(t *testing.T) {
	exercise.SetArchiveLimits(t, 1<<20, 16, 1<<20, 1000)
	_, err := exportUnder(t)
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err(), "common.bin (20 bytes) is over a 16-byte entry cap")
}

func TestExportExerciseArchive_UnpackedOverTheTotal(t *testing.T) {
	exercise.SetArchiveLimits(t, 1<<20, 1<<20, 64, 1000)
	_, err := exportUnder(t)
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err())
}

func TestExportExerciseArchive_TooManyEntries(t *testing.T) {
	exercise.SetArchiveLimits(t, 1<<20, 1<<20, 1<<20, 4)
	_, err := exportUnder(t)
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err(), "four files, exercise.json and manifest.json are six entries")
}

func TestExportExerciseArchive_ArchiveOverTheCap(t *testing.T) {
	exercise.SetArchiveLimits(t, 512, 1<<20, 1<<20, 1000)
	_, err := exportUnder(t)
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err())
}

// What the export writes within the limits, the import reads within the same limits.
func TestExportExerciseArchive_WithinTheLimitsImports(t *testing.T) {
	data, err := exportUnder(t)
	require.NoError(t, err)
	exercise.SetArchiveLimits(t, int64(len(data)), 1<<20, 1<<20, 6)
	_, err = exercise.ParseExerciseArchive(data, "")
	require.NoError(t, err)
}

func TestBundleExerciseArchives_OverTheLimits(t *testing.T) {
	data, err := exportUnder(t)
	require.NoError(t, err)
	items := []exercise.NamedArchive{{Name: "a", Archive: data}, {Name: "b", Archive: data}}
	// Each task (six entries) fits; together they are 2 x 5 files and one root manifest.json.
	exercise.SetArchiveLimits(t, 1<<20, 1<<20, 1<<20, 10)
	_, err = exercise.BundleExerciseArchives(items, "")
	require.ErrorIs(t, err, exerciseModel.ErrArchiveTooLarge.Err())
}
