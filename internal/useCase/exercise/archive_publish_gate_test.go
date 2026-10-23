package exercise_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
)

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

// importRejected imports one task folder under a resources policy (the default when zero). The repository and
// the media expect nothing: a rejected import writes nothing and uploads nothing.
func importRejected(t *testing.T, files map[string][]byte, policy resourcesModel.Policy) (*fakeMedia, error) {
	t.Helper()
	archive, err := exercise.BuildArchiveNoManifest(files)
	require.NoError(t, err)
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	media := newFakeMedia()
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: media, Resources: policy})
	_, err = uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	return media, err
}

func publicContext(t *testing.T, err error) map[string]any {
	t.Helper()
	var withContext interface{ PublicContext() map[string]any }
	require.ErrorAs(t, err, &withContext)
	return withContext.PublicContext()
}

// A published version with a device above the frame and no approved elevation (a new task has none) is
// refused like PublishDraft refuses it.
func TestImportExerciseArchive_PublishedNeedsElevation(t *testing.T) {
	media, err := importRejected(t, examplePreset(t, "xlarge", false), resourcesModel.Policy{})
	require.ErrorIs(t, err, exerciseModel.ErrDevicesNeedElevation.Err())
	ctx := publicContext(t, err)
	require.Equal(t, "versions[1]", ctx["field"])
	require.Contains(t, ctx["devices"], "web")
	require.Empty(t, media.uploads, "nothing is uploaded before the gate")
}

func TestImportExerciseArchive_PublishedAboveCeiling(t *testing.T) {
	// A platform whose ceiling is its frame: xlarge is offered by name but above what any approval may give.
	policy := resourcesModel.DefaultPolicy()
	large, ok := policy.Preset("large")
	require.True(t, ok)
	policy.CeilingBlocks, policy.Ceiling = large.Blocks, large.Amount
	_, importErr := importRejected(t, examplePreset(t, "xlarge", false), policy)
	require.ErrorIs(t, importErr, exerciseModel.ErrDeviceResourcesAboveCeiling.Err())
	require.Equal(t, "versions[1]", publicContext(t, importErr)["field"])
}

func TestImportExerciseArchive_PublishedUnknownPreset(t *testing.T) {
	_, err := importRejected(t, examplePreset(t, "huge", false), resourcesModel.Policy{})
	require.ErrorIs(t, err, exerciseModel.ErrDeviceResourcePresetInvalid.Err())
	ctx := publicContext(t, err)
	require.Equal(t, "versions[1].variants[1].topology.devices[1]", ctx["field"])
	require.Equal(t, "web", ctx["device"])
	require.Empty(t, ctx["variant"], "the variant id of a task not yet created means nothing to the author")
}

// The same content as a draft keeps the draft rules: it imports, the preset is checked only at publication.
func TestImportExerciseArchive_DraftSkipsPublishGate(t *testing.T) {
	for _, preset := range []string{"xlarge", "huge"} {
		t.Run(preset, func(t *testing.T) {
			archive, err := exercise.BuildArchiveNoManifest(examplePreset(t, preset, true))
			require.NoError(t, err)
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			expectUserNames(q)
			expectFilesAdoptedAsIs(q)
			q.EXPECT().CreateExercise(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.CreateExerciseParams) (postgres.Exercise, error) {
					return postgres.Exercise{ID: arg.ID, Name: arg.Name, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
				})
			q.EXPECT().InsertImportedExerciseVersion(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, arg postgres.InsertImportedExerciseVersionParams) (postgres.ExerciseVersion, error) {
					require.EqualValues(t, exerciseModel.VersionStatusDraft, arg.Status)
					return postgres.ExerciseVersion{ID: arg.ID, ExerciseID: arg.ExerciseID, Status: arg.Status, Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
				})
			q.EXPECT().SetImportedExercisePointers(gomock.Any(), gomock.Any()).Return(nil)
			uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Media: newFakeMedia()})
			_, err = uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
			require.NoError(t, err)
		})
	}
}

// A published version inside the frame passes the gate (the example itself uses micro).
func TestImportExerciseArchive_PublishedInsideFramePasses(t *testing.T) {
	_, written, err := importExample(t, examplePreset(t, "large", false))
	require.NoError(t, err)
	require.Len(t, written, 1)
}

// The publish gate runs on every published version: a version validation error names the version too.
func TestImportExerciseArchive_PublishValidationNamesVersion(t *testing.T) {
	archive := mustExerciseArchive(t, `{"name":"broken"}`,
		`[{"status":"draft","variants":[{"tasks":[{"name":"t"}]}]},{"status":"published"}]`)
	err := importArchive(t, archive, "")
	require.ErrorIs(t, err, exerciseModel.ErrVersionNoVariants.Err())
	require.Equal(t, "versions[2]", publicContext(t, err)["field"])
}
