package exercise_test

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/secret"
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

// A file over 1 MiB that deflates past 200:1 is written uncompressed, so the export never writes what the import's
// ratio rule refuses; everything else stays deflated.
func TestExportThenImport_HighlyCompressibleFile(t *testing.T) {
	zeros := make([]byte, 3<<20)
	media := exportFixtureMedia()
	media.files[fileOnce] = storedFile{name: "zeros.bin", body: zeros}
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	for _, opts := range []exercise.ExportOptions{{}, {IncludeSecrets: true, Password: "pw"}} {
		q := postgresMocks.NewMockQuerier(gomock.NewController(t))
		expectExport(t, q, exportExerciseID, "Експорт", exportFixtureVersions())
		uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: media, Cipher: cipher})
		data, err := uc.ExportExerciseArchive(context.Background(), exportExerciseID, opts)
		require.NoError(t, err)
		parsed, err := exercise.ParseExerciseArchive(data, opts.Password)
		require.NoError(t, err, "protected=%v", opts.IncludeSecrets)
		found := false
		for _, body := range parsed.Files {
			found = found || bytes.Equal(body, zeros)
		}
		require.True(t, found, "the zeros file comes back whole")
	}
}

func TestBuildArchive_StoresOnlyWhatWouldBreakTheRatio(t *testing.T) {
	var text []byte // ~1.5 MiB of numbered log lines: compresses well, but far under 200:1
	for i := 0; len(text) < 3<<19; i++ {
		text = fmt.Appendf(text, "%07d request served in %d ms\n", i, i*7919%1000)
	}
	data, err := exercise.BuildArchiveV1(map[string][]byte{"zeros.bin": make([]byte, 2<<20), "log.txt": text})
	require.NoError(t, err)
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	methods := map[string]uint16{}
	for _, f := range reader.File {
		methods[f.Name] = f.Method
	}
	require.Equal(t, zip.Store, methods["zeros.bin"])
	require.Equal(t, zip.Deflate, methods["log.txt"])
	require.Equal(t, zip.Deflate, methods["manifest.json"])
	_, err = exercise.ReadArchiveV1(bytes.NewReader(data))
	require.NoError(t, err)

	protected, err := exercise.BuildProtectedArchiveV1(map[string][]byte{"zeros.bin": make([]byte, 2<<20)}, "pw")
	require.NoError(t, err)
	_, err = exercise.ReadProtectedArchiveV1(protected, "pw")
	require.NoError(t, err)
}
