package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type pausedDraftReferences struct {
	q       exercise.CatalogRepository
	entered chan struct{}
	release chan struct{}
}

func (p *pausedDraftReferences) ReplaceFileReferences(ctx context.Context, in postgres.ReplaceFileReferencesParams) error {
	close(p.entered)
	select {
	case <-p.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return p.q.ReplaceFileReferences(ctx, in)
}

type pausedCatalogRepository struct {
	exercise.CatalogRepository
	pause *pausedDraftReferences
}

func (q pausedCatalogRepository) ReplaceFileReferences(ctx context.Context, in postgres.ReplaceFileReferencesParams) error {
	q.pause.q = q.CatalogRepository
	return q.pause.ReplaceFileReferences(ctx, in)
}

type pausedCatalogWorker struct {
	worker postgres.IUnitOfWorker[exercise.CatalogRepository]
	pause  *pausedDraftReferences
}

func (w pausedCatalogWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	ctx, q, unit, err := w.worker.UnitOfWork(ctx)
	return ctx, pausedCatalogRepository{q, w.pause}, unit, err
}

type pausedPublishQueries struct {
	*postgres.Queries
	entered, release chan struct{}
}

func (q *pausedPublishQueries) PublishValidatedExerciseDraft(ctx context.Context, in postgres.PublishValidatedExerciseDraftParams) (postgres.PublishValidatedExerciseDraftRow, error) {
	close(q.entered)
	select {
	case <-q.release:
	case <-ctx.Done():
		return postgres.PublishValidatedExerciseDraftRow{}, ctx.Err()
	}
	return q.Queries.PublishValidatedExerciseDraft(ctx, in)
}

func TestPublishCannotPromoteContentReplacedAfterValidation(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	repo := exerciseRepo.New(db.Queries)
	entity := mustCreateExercise(t, repo, "Publish validation serialization")
	valid := itMetadataVariants()
	valid.Variants[0].ID = uuid.Must(uuid.NewV7())
	valid.Variants[0].Tasks[0].Description = json.RawMessage(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"text","text":"Solve the task"}]}]}}`)
	_, err := repo.UpsertDraft(ctx, entity.ID, uuid.Must(uuid.NewV7()), valid, itNow, uuid.NullUUID{})
	require.NoError(t, err)
	paused := &pausedPublishQueries{Queries: db.Queries, entered: make(chan struct{}), release: make(chan struct{})}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: paused})
	result := make(chan error, 1)
	go func() { _, err := uc.PublishDraft(ctx, entity.ID); result <- err }()
	select {
	case <-paused.entered:
	case err = <-result:
		t.Fatalf("Publish failed before transition: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	// Import permits incomplete drafts. Replace after Publish validated the
	// former content, before its final transition SQL obtains the family lock.
	_, err = repo.UpsertDraft(ctx, entity.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{AdminNote: "incomplete imported draft"}, time.Now(), uuid.NullUUID{})
	require.NoError(t, err)
	close(paused.release)
	require.Error(t, <-result, "Publish must not promote an unvalidated replacement")
	current, err := repo.GetByID(ctx, entity.ID)
	require.NoError(t, err)
	require.False(t, current.PublishedVersionID.Valid)
}

func TestMissingCatalogTransactionWiringNeverFallsBackToAutocommit(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	e := mustCreateExercise(t, repo, "Required transaction wiring")
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries})
	_, err := uc.SaveDraft(ctx, e.ID, exercise.SaveDraftInput{AdminNote: "must not persist"})
	require.Error(t, err)
	_, err = repo.GetDraft(ctx, e.ID)
	require.Error(t, err)
	current, err := repo.GetByID(ctx, e.ID)
	require.NoError(t, err)
	require.False(t, current.DraftVersionID.Valid)
}

// An ordinary autosave used to commit its content before its references. An import
// in that gap could commit a complete replacement, then lose only its references
// when the old save resumed. Both writers must serialize the whole snapshot.
func TestSaveDraftAndReplacementSerializeContentWithReferences(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Autosave reference serialization")
	author := mustSeedUser(t, db, "draft-atomic@test.test")
	ordinaryFile, err := db.Queries.CreateFile(ctx, postgres.CreateFileParams{ID: uuid.Must(uuid.NewV7()), Name: "ordinary.txt", ContentType: "text/plain", SizeBytes: 1, ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", CreatedAt: itNow, CreatedBy: uuid.NullUUID{UUID: author, Valid: true}})
	require.NoError(t, err)
	file, err := db.Queries.CreateFile(ctx, postgres.CreateFileParams{ID: uuid.Must(uuid.NewV7()), Name: "imported.txt", ContentType: "text/plain", SizeBytes: 1, ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", CreatedAt: itNow})
	require.NoError(t, err)
	paused := &pausedDraftReferences{entered: make(chan struct{}), release: make(chan struct{})}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, CatalogUoW: pausedCatalogWorker{postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool)), paused}})
	saved := make(chan error, 1)
	ordinary := itMetadataVariants()
	ordinary.Variants[0].Tasks[0].Attachments = []exerciseModel.AttachmentRef{{FileID: ordinaryFile.ID, Name: ordinaryFile.Name}}
	go func() {
		_, err := uc.SaveDraft(ctx, ex.ID, exercise.SaveDraftInput{SavedBy: author, AdminNote: "ordinary save", Variants: ordinary.Variants})
		saved <- err
	}()
	select {
	case <-paused.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	imported := make(chan error, 1)
	go func() {
		tx, err := db.Pool.Begin(ctx)
		if err != nil {
			imported <- err
			return
		}
		defer tx.Rollback(ctx)
		q := db.Queries.WithTx(tx)
		_, err = tx.Exec(ctx, `SELECT id FROM exercises WHERE id=$1 FOR UPDATE`, ex.ID)
		if err == nil {
			content := itMetadataVariants()
			content.AdminNote = "imported replacement"
			content.Variants[0].Tasks[0].Attachments = []exerciseModel.AttachmentRef{{FileID: file.ID, Name: file.Name}}
			var draft exerciseModel.ExerciseVersion
			draft, err = exerciseRepo.New(q).UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, time.Now(), uuid.NullUUID{})
			if err == nil {
				err = q.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: "exercise_version", RefID: draft.ID, FileIds: []uuid.UUID{file.ID}})
			}
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
		imported <- err
	}()
	// Before the correction the replacement commits here while the save is paused.
	// A bounded observation allows the corrected writer to hold its parent lock.
	select {
	case err = <-imported:
		require.NoError(t, err)
		imported = nil
	case <-time.After(250 * time.Millisecond):
	}
	close(paused.release)
	require.NoError(t, <-saved)
	if imported != nil {
		require.NoError(t, <-imported)
	}
	draft, err := repo.GetDraft(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, "imported replacement", draft.AdminNote)
	refs, err := db.Queries.GetFileReferenceIDs(ctx, postgres.GetFileReferenceIDsParams{RefType: "exercise_version", RefID: draft.ID})
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{file.ID}, refs, "an acknowledged replacement must retain the files in its content")
}
