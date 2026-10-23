package exercise

import (
	"bytes"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestReimportSourceIDIsSeparateFromTheNewIdentity(t *testing.T) {
	source := uuid.Must(uuid.NewV7())
	data, err := BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"id":"` + source.String() + `","name":"Portable"},"versions":[{"status":"draft","variants":[]}]}`)})
	require.NoError(t, err)
	parsed, err := ParseExerciseArchive(data, "", DefaultLimits())
	require.NoError(t, err)
	require.Equal(t, source, parsed.SourceExerciseID)
	require.Equal(t, uuid.Nil, parsed.Exercise.ID, "a source id is only an update suggestion, never a new identity")
	require.Equal(t, ".", parsed.ItemKey)
}

func TestReimportItemsKeepStableFolderKeysEvenWithRepeatedSourceIDs(t *testing.T) {
	source := uuid.Must(uuid.NewV7())
	doc := []byte(`{"schema":"cib-exercise/1","exercise":{"id":"` + source.String() + `","name":"Portable"},"versions":[{"status":"draft","variants":[]}]}`)
	data, err := BuildArchiveV1(map[string][]byte{"z/exercise.json": doc, "a/nested/exercise.json": doc})
	require.NoError(t, err)
	entries, protected, err := openZipAt(bytes.NewReader(data), int64(len(data)), "", DefaultLimits())
	require.NoError(t, err)
	read, err := readArchiveEntries(entries, protected, DefaultLimits())
	require.NoError(t, err)
	items, err := parseArchiveRead(read)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "a/nested", items[0].ItemKey)
	require.Equal(t, "z", items[1].ItemKey)
	require.Equal(t, source, items[0].SourceExerciseID)
	require.Equal(t, source, items[1].SourceExerciseID)
}

func TestReimportSourceIDMissingOrMalformedKeepsLegacyNewImportCompatibility(t *testing.T) {
	for _, source := range []string{"", "foreign-non-uuid-id"} {
		t.Run(source, func(t *testing.T) {
			data, err := BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"id":"` + source + `","name":"Portable"},"versions":[{"status":"draft","variants":[]}]}`)})
			require.NoError(t, err)
			parsed, err := ParseExerciseArchive(data, "", DefaultLimits())
			require.NoError(t, err)
			require.Equal(t, uuid.Nil, parsed.SourceExerciseID)
		})
	}
}

func TestReimportWorkingVersionChoosesDraftThenPublishedThenFirstHistory(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []exerciseModel.VersionStatus
		want     int
	}{
		{"draft wins", []exerciseModel.VersionStatus{"checkpoint", "published", "draft"}, 2},
		{"published fallback", []exerciseModel.VersionStatus{"checkpoint", "unpublished", "published"}, 2},
		{"first history fallback", []exerciseModel.VersionStatus{"checkpoint", "unpublished"}, 0},
		{"no version", nil, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			versions := make([]exerciseModel.ExerciseVersion, len(tc.statuses))
			for i, status := range tc.statuses {
				versions[i].Status = status
			}
			require.Equal(t, tc.want, workingImportVersion(versions))
		})
	}
}

func TestReimportParsesEveryItemBeforeReturningAny(t *testing.T) {
	data, err := BuildArchiveV1(map[string][]byte{
		"a/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Valid"},"versions":[{"status":"draft","variants":[]}]}`),
		"z/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Invalid"},"versions":[{"variants":[]}]}`),
	})
	require.NoError(t, err)
	entries, protected, err := openZipAt(bytes.NewReader(data), int64(len(data)), "", DefaultLimits())
	require.NoError(t, err)
	read, err := readArchiveEntries(entries, protected, DefaultLimits())
	require.NoError(t, err)
	items, err := parseArchiveRead(read)
	require.Error(t, err)
	require.Nil(t, items)
}
