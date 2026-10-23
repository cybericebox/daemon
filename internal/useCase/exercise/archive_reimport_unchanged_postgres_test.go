package exercise_test

import (
	"context"
	"testing"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Importing the same archive again changes nothing: the update is reported as unchanged and the working copy
// (its version, its edits) stays exactly as it was.
func TestReimportOfTheSameContentLeavesTheDraftAlone(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "same-import@test.test", importJobT0)
	user.Role = rbac.RoleSuperAdmin
	_, err := userRepo.New(db.Queries).Create(ctx, user)
	require.NoError(t, err)
	repo := exerciseRepo.New(db.Queries)
	entity, err := exerciseModel.NewExercise("Same task", "", nil, user.ID, importJobT0)
	require.NoError(t, err)
	entity, err = repo.Create(ctx, entity)
	require.NoError(t, err)
	archive, err := exercise.BuildArchiveV1(map[string][]byte{
		"a/exercise.json": []byte(`{"schema":"cib-exercise/1","exercise":{"name":"Same task","description":"d","tags":["web"]},"versions":[{"status":"draft","admin_note":"n","variants":[{"tasks":[{"name":"One","flag":["ICE{same}"],"attachments":[{"file":"files/a.txt"}]}]}]}]}`),
		"a/files/a.txt":   []byte("same file"),
	})
	require.NoError(t, err)
	sources := newPostgresSources(t, db)
	var worker postgres.IUnitOfWorker[exercise.CatalogRepository] = postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Media: postgresArchiveMedia{newFakeMedia(), db.Queries}, CatalogUoW: worker})
	uc.SetArchiveImport(exercise.ArchiveImportDependencies{Sources: sources})
	actor := exercise.Actor{UserID: user.ID, Role: rbac.RoleSuperAdmin}

	commit := func(upload uuid.UUID) exercise.ArchiveImportStart {
		t.Helper()
		sources.stage(upload, user.ID, archive, mediaModel.UploadPurposeExerciseArchive)
		in := exercise.PreviewArchiveInput{UploadID: upload, Targets: []exercise.ImportTarget{{ItemKey: "a", Mode: "update", ExerciseID: entity.ID}}}
		preview, err := uc.PreviewArchiveImport(ctx, actor, in)
		require.NoError(t, err)
		current, _ := repo.GetDraft(ctx, entity.ID)
		result, err := uc.CommitArchiveImport(ctx, actor, exercise.CommitArchiveInput{PreviewArchiveInput: in,
			ConfirmedUpdates: []exercise.ConfirmedImportUpdate{{ItemKey: "a", Revision: preview.Items[0].ConfirmationRevision, ReplaceDraft: current.ID != uuid.Nil}}})
		require.NoError(t, err)
		return result
	}

	first := commit(uuid.Must(uuid.NewV7()))
	require.Equal(t, 1, first.Updated)
	require.Equal(t, 0, first.Unchanged)
	before, err := repo.GetDraft(ctx, entity.ID)
	require.NoError(t, err)
	beforeEntity, err := repo.GetByID(ctx, entity.ID)
	require.NoError(t, err)

	second := commit(uuid.Must(uuid.NewV7()))
	require.Equal(t, 0, second.Updated)
	require.Equal(t, 1, second.Unchanged)
	require.Equal(t, exercise.ImportModeUnchanged, second.Results[0].Mode)
	require.Equal(t, entity.ID, second.Results[0].ExerciseID)
	after, err := repo.GetDraft(ctx, entity.ID)
	require.NoError(t, err)
	require.Equal(t, before.ID, after.ID)
	require.Equal(t, before.CreatedAt, after.CreatedAt, "the working copy is not rewritten")
	afterEntity, err := repo.GetByID(ctx, entity.ID)
	require.NoError(t, err)
	require.Equal(t, beforeEntity.UpdatedAt, afterEntity.UpdatedAt, "the task is not touched")
}
