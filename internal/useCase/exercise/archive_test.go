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
		"exercise.json": []byte(`{"format":"cib-exercise/v1"}`),
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
		"first/exercise.json":  []byte(`{"format":"cib-exercise/v1"}`),
		"first/files/a.txt":    []byte("a"),
		"second/exercise.json": []byte(`{"format":"cib-exercise/v1"}`),
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
	owner := uuid.Must(uuid.NewV7())
	sourceExercise := exerciseModel.Exercise{Name: "portable task", Description: "d", Tags: []string{"web"}}
	sourceVersion := exerciseModel.ExerciseVersion{Status: exerciseModel.VersionStatusDraft, Variants: []exerciseModel.Variant{{
		ID: uuid.Must(uuid.NewV7()), Tasks: []exerciseModel.Task{{ID: uuid.Must(uuid.NewV7()), Name: "task", Difficulty: exerciseModel.DifficultyEasy}},
	}}}
	payload, err := json.Marshal(struct {
		Format   string                          `json:"format"`
		Exercise exerciseModel.Exercise          `json:"exercise"`
		Versions []exerciseModel.ExerciseVersion `json:"versions"`
	}{Format: "cib-exercise/v1", Exercise: sourceExercise, Versions: []exerciseModel.ExerciseVersion{sourceVersion}})
	require.NoError(t, err)
	archive, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": payload})
	require.NoError(t, err)

	q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
			return postgres.Exercise{ID: arg.ID, Name: arg.Name, Description: arg.Description, Tags: arg.Tags, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy, UpdatedAt: arg.UpdatedAt, UpdatedBy: arg.UpdatedBy}, nil
		})
	q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
			if arg.ExerciseID == sourceExercise.ID || arg.ID == sourceVersion.ID {
				t.Fatal("source IDs must not be reused")
			}
			return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, AdminNote: arg.AdminNote, Variants: arg.Variants, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy, PublishedAt: arg.PublishedAt}, nil
		})
	q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
	imported, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive, CreatedBy: owner})
	require.NoError(t, err)
	require.NotEqual(t, sourceExercise.ID, imported.ID)
	require.Equal(t, "portable task", imported.Name)
	require.WithinDuration(t, time.Now(), imported.CreatedAt, time.Minute)
}

func mustExerciseArchive(t *testing.T, source exerciseModel.Exercise, versions []exerciseModel.ExerciseVersion) []byte {
	t.Helper()
	payload, err := json.Marshal(struct {
		Format   string                          `json:"format"`
		Exercise exerciseModel.Exercise          `json:"exercise"`
		Versions []exerciseModel.ExerciseVersion `json:"versions"`
	}{Format: "cib-exercise/v1", Exercise: source, Versions: versions})
	require.NoError(t, err)
	archive, err := exercise.BuildArchiveV1(map[string][]byte{"exercise.json": payload})
	require.NoError(t, err)
	return archive
}

func TestImportExerciseArchive_AcceptsIncompleteDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := mustExerciseArchive(t, exerciseModel.Exercise{Name: "half done"}, []exerciseModel.ExerciseVersion{{
		Status:   exerciseModel.VersionStatusDraft,
		Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Name: ""}}}},
	}})
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
	archive := mustExerciseArchive(t, exerciseModel.Exercise{Name: "broken"}, []exerciseModel.ExerciseVersion{{
		Status: exerciseModel.VersionStatusPublished,
	}})
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
	require.Contains(t, string(files["exercise.json"]), `"Label":"Before refactor"`)
}

func TestImportExerciseArchive_RestoresLabel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	archive := mustExerciseArchive(t, exerciseModel.Exercise{Name: "labelled"}, []exerciseModel.ExerciseVersion{{
		Status: exerciseModel.VersionStatusCheckpoint, Label: "Before refactor",
	}})
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
	archive := mustExerciseArchive(t, exerciseModel.Exercise{Name: "labelled"}, []exerciseModel.ExerciseVersion{
		{Status: exerciseModel.VersionStatusDraft, Label: "must not survive import"},
		{Status: exerciseModel.VersionStatusCheckpoint, Label: longLabel},
	})
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
