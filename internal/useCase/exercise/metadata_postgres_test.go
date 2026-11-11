package exercise_test

import (
	"context"
	"testing"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestNewImportMetadataKeepsTheAllocatedCopyNameOnTheDraft(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	uc := newUC(db.Queries, newFakeMedia())
	archive := mustExerciseArchive(t, `{"name":"Imported original","description":"known baseline","tags":["web"]}`,
		`[{"status":"draft","variants":[]},{"status":"checkpoint","exercise":{"name":"Historic authored name","description":"historic description","tags":["misc"]},"variants":[]}]`)
	first, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
	second, err := uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)
	require.Equal(t, "Imported original (імпорт 1)", second.Name)
	repo := exerciseRepo.New(db.Queries)
	identity, err := repo.GetByID(context.Background(), second.ID)
	require.NoError(t, err)
	require.Equal(t, second.Name, identity.Name)
	draft, err := repo.GetDraft(context.Background(), second.ID)
	require.NoError(t, err)
	require.Equal(t, second.Name, draft.Name)
	versions, err := repo.ListVersions(context.Background(), second.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1, "the checkpoint of the archive is left out: an import brings one draft")
	require.True(t, versions[0].Status.IsDraft())
}

func TestOrdinaryMetadataPatchCreatesACompleteDraftWithoutPublishing(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	uc := newUC(db.Queries, newFakeMedia())
	ctx := context.Background()
	created, err := uc.CreateExercise(ctx, exercise.CreateExerciseInput{Name: "Original public name", Description: "original", Tags: []string{"web"}})
	require.NoError(t, err)
	updated, err := uc.UpdateExerciseIdentity(ctx, exercise.UpdateExerciseInput{ID: created.ID, Name: "Working rename", Description: "working", Tags: []string{"Crypto"}})
	require.NoError(t, err)
	require.Equal(t, "Working rename", updated.Name)
	require.NotNil(t, updated.DraftVersionID)
	require.True(t, updated.HasChanges)
	draft, err := uc.GetWorkingCopy(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "Working rename", draft.Name)
	require.Equal(t, []string{"crypto"}, draft.Tags)
	require.NotEqual(t, uuid.Nil, draft.ID)
	identity, err := exerciseRepo.New(db.Queries).GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "Original public name", identity.Name)
}

// The archive carries a draft and a published version: only the draft is imported, under its own authored name,
// which is made unique like any imported name. Nothing is published.
func TestNewImportBringsOnlyTheDraftAndAllocatesItsCopyName(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	uc := newUC(db.Queries, newFakeMedia())
	_, err := uc.CreateExercise(ctx, exercise.CreateExerciseInput{Name: "Existing draft name"})
	require.NoError(t, err)
	archive := mustExerciseArchive(t, `{"name":"Legacy root name"}`, `[
		{"status":"draft","exercise":{"name":"Existing draft name","description":"draft description","tags":["crypto"]},"variants":[]},
		{"status":"published","exercise":{"name":"Published authored name","description":"published description","tags":["web"]},"variants":[{"tasks":[{"name":"Published task","difficulty":"easy","flag":["ICE{x}"],"description":{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Solve the task"}]}]}}}]}]},
		{"status":"checkpoint","exercise":{"name":"Historical authored name","tags":["misc"]},"variants":[]}
	]`)
	created, err := uc.ImportExerciseArchive(ctx, exercise.ImportExerciseInput{Archive: archive})
	require.NoError(t, err)
	require.Equal(t, "Existing draft name (імпорт 1)", created.Name)
	repo := exerciseRepo.New(db.Queries)
	identity, err := repo.GetByID(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.Name, identity.Name)
	require.False(t, identity.PublishedVersionID.Valid, "an import never publishes")
	draft, err := repo.GetDraft(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, created.Name, draft.Name)
	require.Equal(t, []string{"crypto"}, draft.Tags)
	versions, err := repo.ListVersions(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, versions, 1)
}
