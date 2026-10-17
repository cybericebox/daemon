package exercise_test

import (
	"bytes"
	"context"
	"encoding/json"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

func TestArchiveV1_RoundTripAndChecksum(t *testing.T) {
	data, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(`{"name":"web"}`), "files/a.txt": []byte("payload")})
	require.NoError(t, err)
	got, err := exercise.ReadArchiveV1(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, []byte("payload"), got["files/a.txt"])
}

func TestProtectedZipArchive_RoundTripRejectsWrongPassword(t *testing.T) {
	protected, err := exercise.BuildProtectedArchiveV1(map[string][]byte{
		"exercise.json": []byte(`{"schema":"cib-exercise/1"}`),
		"files/a.txt":   []byte("private attachment"),
	}, "strong password")
	require.NoError(t, err)
	restored, err := exercise.ReadProtectedArchiveV1(protected, "strong password")
	require.NoError(t, err)
	require.Equal(t, []byte("private attachment"), restored["files/a.txt"])
	_, err = exercise.ReadProtectedArchiveV1(protected, "wrong")
	require.Error(t, err)
}

func TestExpandExerciseArchiveBundle_RestoresFolderArchives(t *testing.T) {
	bundle, err := exercise.BuildArchiveV1(map[string][]byte{
		"first/exercise.json":  []byte(`{"schema":"cib-exercise/1"}`),
		"first/files/a.txt":    []byte("a"),
		"second/exercise.json": []byte(`{"schema":"cib-exercise/1"}`),
	})
	require.NoError(t, err)
	imports, err := exercise.ExpandExerciseArchiveBundle(bundle, "")
	require.NoError(t, err)
	require.Len(t, imports, 2)
	for _, item := range imports {
		files, readErr := exercise.ReadArchiveV1(bytes.NewReader(item.Archive))
		require.NoError(t, readErr)
		require.Contains(t, files, "exercise.json")
	}
}

func TestImportExerciseArchive_ReassignsIdentityAndVersionIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	expectUserNames(q)
	owner := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000000aa")
	sourceID := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-000000000001")
	archive := mustExerciseArchive(t, `{"id":"`+sourceID.String()+`","name":"portable task","description":"d","tags":["web"]}`,
		`[{"id":"`+sourceID.String()+`","status":"draft","variants":[{"tasks":[{"name":"task","difficulty":"easy"}]}]}]`)

	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, Description: arg.Description, Tags: arg.Tags, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy, UpdatedAt: arg.UpdatedAt, UpdatedBy: arg.UpdatedBy}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			if arg.ExerciseID == sourceID || arg.ID == sourceID {
				t.Fatal("source IDs must not be reused")
			}
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, AdminNote: arg.AdminNote, Variants: arg.Variants, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy, PublishedAt: arg.PublishedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	imported, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive, CreatedBy: owner})
	require.NoError(t, err)
	require.NotEqual(t, sourceID, imported.ID)
	require.Equal(t, "portable task", imported.Name)
	require.WithinDuration(t, time.Now(), imported.CreatedAt, time.Minute)
}

// mustExerciseArchive wraps schema-1 exercise and versions JSON fragments into a zip.
func mustExerciseArchive(t *testing.T, exerciseJSON, versionsJSON string) []byte {
	t.Helper()
	payload := `{"schema":"cib-exercise/1","exercise":` + exerciseJSON + `,"versions":` + versionsJSON + `}`
	archive, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": []byte(payload)})
	require.NoError(t, err)
	return archive
}

func TestImportExerciseArchive_AcceptsIncompleteDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := mustExerciseArchive(t, `{"name":"half done"}`, `[{"status":"draft","variants":[{"tasks":[{"name":""}]}]}]`)
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
}

func TestImportExerciseArchive_RejectsUnpublishablePublishedVersion(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := mustExerciseArchive(t, `{"name":"broken"}`, `[{"status":"published"}]`)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.ErrorIs(t, err, exerciseModel.ErrVersionNoVariants.Err())
}

func TestExportExerciseArchive_OmitsRegenerateFlags(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	exID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, Name: "portable"}, nil)
	q.EXPECT().ListExerciseVersions(gomock.Any(), exID).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "published", Variants: []byte(`[]`)},
	}, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	data, err := uc.ExportExerciseArchive(context.Background(), exID, exercise.ExportOptions{})
	require.NoError(t, err)
	files, err := exercise.ReadArchiveV1(bytes.NewReader(data))
	require.NoError(t, err)
	require.NotContains(t, string(files["exercise.json"]), "RegenerateFlagsOnPublish")
}

func TestExportExerciseArchive_IncludesLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	exID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, Name: "portable"}, nil)
	q.EXPECT().ListExerciseVersions(gomock.Any(), exID).Return([]postgres.ExerciseVersion{
		{ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "checkpoint", Label: "Before refactor", Variants: []byte(`[]`)},
	}, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	data, err := uc.ExportExerciseArchive(context.Background(), exID, exercise.ExportOptions{})
	require.NoError(t, err)
	files, err := exercise.ReadArchiveV1(bytes.NewReader(data))
	require.NoError(t, err)
	var doc struct {
		Versions []struct {
			Label string `json:"label"`
		} `json:"versions"`
	}
	require.NoError(t, json.Unmarshal(files["exercise.json"], &doc))
	require.Equal(t, "Before refactor", doc.Versions[0].Label)
}

func TestImportExerciseArchive_RestoresLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := mustExerciseArchive(t, `{"name":"labelled"}`, `[{"status":"checkpoint","label":"Before refactor"}]`)
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			if arg.Label != "Before refactor" {
				t.Fatalf("label must survive import: %+v", arg)
			}
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Label: arg.Label, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
}

func TestImportExerciseArchive_EnforcesLabelInvariant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	longLabel := strings.Repeat("a", 600)
	archive := mustExerciseArchive(t, `{"name":"labelled"}`,
		`[{"status":"draft","label":"must not survive import"},{"status":"checkpoint","label":"`+longLabel+`"}]`)
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			switch arg.Status {
			case string(exerciseModel.VersionStatusDraft):
				if arg.Label != "" {
					t.Fatalf("draft label must be forced to empty on import, got %q", arg.Label)
				}
			case string(exerciseModel.VersionStatusCheckpoint):
				if got := len([]rune(arg.Label)); got != 500 {
					t.Fatalf("checkpoint label must be truncated to 500 runes, got %d", got)
				}
			default:
				t.Fatalf("unexpected version status %q", arg.Status)
			}
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Label: arg.Label, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
}

func TestImportExerciseArchive_Schema1Published(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := mustExerciseArchive(t, `{"name":"Опублікована"}`, `[{"status":"published","variants":[{"tasks":[{
		"name":"Тест","difficulty":"easy","flag":["ICE{x}"],
		"description":{"root":{"type":"text","text":"Опис"}},
		"hints":[{"text":"підказка"}]}]}]}]`)
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).Times(1).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			require.Equal(t, string(exerciseModel.VersionStatusPublished), arg.Status)
			var variants []exerciseModel.Variant
			require.NoError(t, json.Unmarshal(arg.Variants, &variants))
			task := variants[0].Tasks[0]
			require.NotEqual(t, uuid.Nil, task.ID)
			require.NotEqual(t, uuid.Nil, task.Hints[0].ID)
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	_, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
}

func TestExportExerciseArchive_IsImportableSchema1(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	exID := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-000000000001")
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID, Name: "portable"}, nil)
	q.EXPECT().ListExerciseVersions(gomock.Any(), exID).Return([]postgres.ExerciseVersion{
		{ID: exID, ExerciseID: exID, Status: "draft", Variants: []byte(`[]`)},
	}, nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})

	data, err := uc.ExportExerciseArchive(context.Background(), exID, exercise.ExportOptions{})
	require.NoError(t, err)
	files, err := exercise.ReadArchiveV1(bytes.NewReader(data))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(string(files["exercise.json"]), "{\n  \"schema\": \"cib-exercise/1\","))
	parsed, err := exercise.ParseExerciseArchive(data, "")
	require.NoError(t, err)
	require.Equal(t, "portable", parsed.Exercise.Name)
}

func TestImportExerciseArchive_V2AttachmentUploaded(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	doc := "schema: cib-exercise/1\nexercise:\n  name: З файлами\nversions:\n  - status: draft\n    variants:\n      - tasks:\n" +
		"          - name: перше\n            attachments:\n              - {file: files/dump.bin, name: Дамп.pcap}\n" +
		"          - name: друге\n            attachments:\n              - {file: shared/copy.bin}\n"
	archive, err := exercise.BuildArchiveNoManifest(map[string][]byte{
		"exercise.yaml":   []byte(doc),
		"files/dump.bin":  []byte("packets"),
		"shared/copy.bin": []byte("packets"),
	})
	require.NoError(t, err)
	expectFilesAdoptedAsIs(q)
	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	media := newFakeMedia()
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			var variants []exerciseModel.Variant
			require.NoError(t, json.Unmarshal(arg.Variants, &variants))
			first, second := variants[0].Tasks[0].Attachments[0], variants[0].Tasks[1].Attachments[0]
			require.Equal(t, media.uploads[0].ID, first.FileID)
			require.Equal(t, first.FileID, second.FileID)
			require.Equal(t, "Дамп.pcap", first.Name)
			require.Equal(t, "copy.bin", second.Name)
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: media})

	_, err = uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
	require.Len(t, media.uploads, 1)
	require.Equal(t, "Дамп.pcap", media.uploads[0].Name)
	require.Equal(t, []byte("packets"), media.uploads[0].Body)
}
