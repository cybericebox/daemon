package exercise_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// importedRows is what one import wrote: the version rows and the pointers set on the exercise.
type importedRows struct {
	versions []postgres.InsertImportedExerciseVersionParams
	pointers postgres.SetImportedExercisePointersParams
}

func importAsWritten(t *testing.T, archive []byte) (importedRows, error) {
	t.Helper()
	var rows importedRows
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	expectUserNames(q)
	expectFilesAdoptedAsIs(q)
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		}).AnyTimes()
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			rows.versions = append(rows.versions, arg)
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		}).AnyTimes()
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.SetImportedExercisePointersParams) error {
			rows.pointers = arg
			return nil
		}).AnyTimes()
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	return rows, err
}

func requireOneDraft(t *testing.T, rows importedRows, adminNote string) {
	t.Helper()
	require.Len(t, rows.versions, 1, "an import brings exactly one version")
	version := rows.versions[0]
	require.EqualValues(t, exerciseModel.VersionStatusDraft, version.Status)
	require.Equal(t, adminNote, version.AdminNote)
	require.False(t, version.PublishedAt.Valid, "an import never stamps a publication time")
	require.Equal(t, uuid.NullUUID{UUID: version.ID, Valid: true}, rows.pointers.DraftVersionID)
	require.False(t, rows.pointers.PublishedVersionID.Valid, "an import never sets the published pointer")
}

const draftBody = `"variants":[{"tasks":[{"name":"t"}]}]`

// A document marked published is imported as a draft, and the publish gate does not run: this one has no
// difficulty, flag or description, which publication would refuse.
func TestImportExerciseArchive_PublishedLandsAsDraft(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"marked published"}`, `[{"status":"published","admin_note":"p",`+draftBody+`}]`)
	rows, err := importAsWritten(t, archive)
	require.NoError(t, err)
	requireOneDraft(t, rows, "p")
}

// With a draft and a published version in the archive only the draft is imported, whatever the order.
func TestImportExerciseArchive_DraftWinsOverPublished(t *testing.T) {
	for name, versions := range map[string]string{
		"draft first":     `[{"status":"draft","admin_note":"d",` + draftBody + `},{"status":"published","admin_note":"p",` + draftBody + `}]`,
		"published first": `[{"status":"published","admin_note":"p",` + draftBody + `},{"status":"draft","admin_note":"d",` + draftBody + `}]`,
	} {
		t.Run(name, func(t *testing.T) {
			rows, err := importAsWritten(t, mustExerciseArchive(t, `{"name":"both"}`, versions))
			require.NoError(t, err)
			requireOneDraft(t, rows, "d")
		})
	}
}

// Without a draft the published version is imported; other versions are ignored, not refused.
func TestImportExerciseArchive_PublishedWinsOverHistory(t *testing.T) {
	versions := `[{"status":"checkpoint","label":"c","admin_note":"c",` + draftBody + `},{"status":"unpublished","admin_note":"u",` + draftBody + `},{"status":"published","admin_note":"p",` + draftBody + `}]`
	rows, err := importAsWritten(t, mustExerciseArchive(t, `{"name":"history"}`, versions))
	require.NoError(t, err)
	requireOneDraft(t, rows, "p")
}

// An archive of checkpoints only: the first one, which an export writes newest first, becomes the draft and loses
// its checkpoint label.
func TestImportExerciseArchive_CheckpointsOnlyImportsNewestAsDraft(t *testing.T) {
	versions := `[{"status":"checkpoint","label":"newest","admin_note":"n",` + draftBody + `},{"status":"checkpoint","label":"older","admin_note":"o",` + draftBody + `}]`
	rows, err := importAsWritten(t, mustExerciseArchive(t, `{"name":"checkpoints"}`, versions))
	require.NoError(t, err)
	requireOneDraft(t, rows, "n")
	require.Empty(t, rows.versions[0].Label)
}

// Several drafts or several published versions are no longer an error: the first one is imported.
func TestImportExerciseArchive_RepeatedStatusTakesTheFirst(t *testing.T) {
	for _, status := range []string{"draft", "published"} {
		t.Run(status, func(t *testing.T) {
			versions := `[{"status":"` + status + `","admin_note":"first",` + draftBody + `},{"status":"` + status + `","admin_note":"second",` + draftBody + `}]`
			rows, err := importAsWritten(t, mustExerciseArchive(t, `{"name":"twice"}`, versions))
			require.NoError(t, err)
			requireOneDraft(t, rows, "first")
		})
	}
}

// The status is still a known key: an unknown value is refused, as before.
func TestImportExerciseArchive_StatusStillValidated(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"bad"}`, `[{"status":"nope",`+draftBody+`}]`)
	_, err := importAsWritten(t, archive)
	require.ErrorIs(t, err, exerciseModel.ErrArchiveInvalid.Err())
}

// Only the schema string cib-exercise/1.0 is read.
func TestImportExerciseArchive_SchemaString(t *testing.T) {
	archiveWith := func(schema string) []byte {
		payload := `{"schema":"` + schema + `","exercise":{"name":"schema check"},"versions":[{"status":"draft",` + draftBody + `}]}`
		archive, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(payload)})
		require.NoError(t, err)
		return archive
	}
	rows, err := importAsWritten(t, archiveWith("cib-exercise/1.0"))
	require.NoError(t, err)
	requireOneDraft(t, rows, "")
	for _, old := range []string{"cib-exercise/1", "cib-exercise/2", "cib-exercise/1.1"} {
		_, err = importAsWritten(t, archiveWith(old))
		require.ErrorIs(t, err, exerciseModel.ErrArchiveSchemaUnsupported.Err(), old)
		require.Equal(t, "cib-exercise/1.0", publicContext(t, err)["expected"])
	}
	require.Contains(t, exerciseModel.ErrArchiveSchemaUnsupported.Err().Error(), "cib-exercise/1.0")
}

func publicContext(t *testing.T, err error) map[string]any {
	t.Helper()
	var withContext interface{ PublicContext() map[string]any }
	require.ErrorAs(t, err, &withContext)
	return withContext.PublicContext()
}

// examplePreset is the example task with the web device on another resource preset and, when draft, as a draft.
func examplePreset(t *testing.T, preset string, draft bool) map[string][]byte {
	t.Helper()
	files := exampleEntries(t)
	doc := files["exercise.yaml"]
	require.Equal(t, 1, bytes.Count(doc, []byte("resource_preset: micro")))
	doc = bytes.Replace(doc, []byte("resource_preset: micro"), []byte("resource_preset: "+preset), 1)
	if draft {
		require.Equal(t, 1, bytes.Count(doc, []byte("- status: published")))
		doc = bytes.Replace(doc, []byte("- status: published"), []byte("- status: draft"), 1)
	}
	return replaced(files, "exercise.yaml", doc)
}

// The resource preset is checked when a version is published, never on import: an oversized or unknown preset
// imports as a draft, whatever status the document gives.
func TestImportExerciseArchive_ResourcePresetsAreNotCheckedOnImport(t *testing.T) {
	for _, preset := range []string{"xlarge", "huge"} {
		for _, draft := range []bool{true, false} {
			archive, err := exercise.BuildArchiveNoManifest(examplePreset(t, preset, draft))
			require.NoError(t, err)
			rows, err := importAsWritten(t, archive)
			require.NoError(t, err, preset)
			require.Len(t, rows.versions, 1)
			require.EqualValues(t, exerciseModel.VersionStatusDraft, rows.versions[0].Status)
		}
	}
}
