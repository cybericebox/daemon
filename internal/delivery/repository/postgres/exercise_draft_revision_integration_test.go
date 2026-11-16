package postgres_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The working copy is replaced only on top of the revision the writer read. The database assigns the revision on every
// change; a write that carries another one (or a first draft written over an existing one) changes nothing.

func newDraftRevisionRepo(t *testing.T) (context.Context, *exerciseRepo.Repository, exerciseModel.Exercise, uuid.UUID) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	repo := exerciseRepo.New(db.Queries)
	author := mustSeedUser(t, db, "revision-author@test.test")
	return context.Background(), repo, mustCreateExercise(t, repo, "Draft revision"), author
}

func contentWithNote(note string) exerciseModel.ExerciseVersion {
	content := itMetadataVariants()
	content.AdminNote = note
	return content
}

func TestDraftRevisionIsAssignedAndMovesOnEveryChange(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	by := uuid.NullUUID{UUID: author, Valid: true}

	first, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("one"), itNow, by, time.Time{})
	require.NoError(t, err)
	require.False(t, first.UpdatedAt.IsZero())
	require.Equal(t, by, first.UpdatedBy)
	require.Equal(t, first.UpdatedAt.Truncate(time.Microsecond), first.UpdatedAt, "microsecond precision, as the API echoes it")

	second, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("two"), itNow, by, first.UpdatedAt)
	require.NoError(t, err)
	require.True(t, second.UpdatedAt.After(first.UpdatedAt), "a change moves the revision forward")
	require.Equal(t, first.ID, second.ID)

	read, err := repo.GetDraftRevision(ctx, ex.ID)
	require.NoError(t, err)
	require.True(t, second.UpdatedAt.Equal(read.UpdatedAt))
}

func TestDraftRevisionStaysWhenNothingChanged(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	author := mustSeedUser(t, db, "revision-author@test.test")
	ex := mustCreateExercise(t, repo, "Draft revision")
	by := uuid.NullUUID{UUID: author, Valid: true}
	// The same content: itVariants gives every task a fresh id, so build it once.
	content := contentWithNote("same")
	first, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, by, time.Time{})
	require.NoError(t, err)
	again, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), content, itNow, by, first.UpdatedAt)
	require.NoError(t, err)
	require.True(t, first.UpdatedAt.Equal(again.UpdatedAt), "a save of identical content by the same author is not a new revision")
}

func TestDraftRevisionRefusesAStaleWriteAndKeepsTheFirst(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	by := uuid.NullUUID{UUID: author, Valid: true}
	base, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("base"), itNow, by, time.Time{})
	require.NoError(t, err)

	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("first"), itNow, by, base.UpdatedAt)
	require.NoError(t, err)
	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("second"), itNow, by, base.UpdatedAt)
	require.ErrorIs(t, err, pgx.ErrNoRows, "the second writer started from a revision that is gone")

	kept, err := repo.GetDraft(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, "first", kept.AdminNote)
}

func TestDraftRevisionOnlyOneOfTwoConcurrentSavesWins(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	by := uuid.NullUUID{UUID: author, Valid: true}
	base, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("base"), itNow, by, time.Time{})
	require.NoError(t, err)

	var wg sync.WaitGroup
	results := make([]error, 2)
	for i, note := range []string{"left", "right"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote(note), itNow, by, base.UpdatedAt)
		}()
	}
	wg.Wait()

	wins := 0
	for _, result := range results {
		if result == nil {
			wins++
		} else {
			require.ErrorIs(t, result, pgx.ErrNoRows)
		}
	}
	require.Equal(t, 1, wins, "exactly one of two saves from the same revision is applied")
}

func TestDraftRevisionAFirstDraftNeverReplacesAnExistingOne(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	by := uuid.NullUUID{UUID: author, Valid: true}
	_, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("theirs"), itNow, by, time.Time{})
	require.NoError(t, err)

	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("mine"), itNow, by, time.Time{})
	require.ErrorIs(t, err, pgx.ErrNoRows, "an editor that saw no working copy must not overwrite one someone created")
	kept, err := repo.GetDraft(ctx, ex.ID)
	require.NoError(t, err)
	require.Equal(t, "theirs", kept.AdminNote)
}

func TestDraftRevisionAWriteToADiscardedCopyDoesNotBringItBack(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	by := uuid.NullUUID{UUID: author, Valid: true}
	base, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("base"), itNow, by, time.Time{})
	require.NoError(t, err)
	_, err = repo.Discard(ctx, ex.ID, base.UpdatedAt)
	require.NoError(t, err)

	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("late"), itNow, by, base.UpdatedAt)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	_, err = repo.GetDraftRevision(ctx, ex.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows, "no working copy was created by the stale write")
}

func TestDraftRevisionDiscardNeedsTheCurrentRevision(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	by := uuid.NullUUID{UUID: author, Valid: true}
	base, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("base"), itNow, by, time.Time{})
	require.NoError(t, err)
	_, err = repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("newer"), itNow, by, base.UpdatedAt)
	require.NoError(t, err)

	_, err = repo.Discard(ctx, ex.ID, base.UpdatedAt)
	require.ErrorIs(t, err, pgx.ErrNoRows, "the editor's copy is older than the saved one")
	_, err = repo.GetDraft(ctx, ex.ID)
	require.NoError(t, err, "the newer working copy is still there")
}

func TestDraftRevisionIdentityEditChecksTheRevisionAndMovesIt(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	by := uuid.NullUUID{UUID: author, Valid: true}
	base, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("base"), itNow, by, time.Time{})
	require.NoError(t, err)
	current, err := repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)

	renamed := current
	renamed.Name = "Renamed once"
	renamed.UpdatedAt, renamed.UpdatedBy = itNow.Add(time.Minute), by
	saved, err := repo.SaveDraftIdentity(ctx, renamed, current.UpdatedAt, uuid.Must(uuid.NewV7()), base.UpdatedAt)
	require.NoError(t, err)
	require.True(t, saved.UpdatedAt.After(base.UpdatedAt))

	current, err = repo.GetByID(ctx, ex.ID)
	require.NoError(t, err)
	renamed = current
	renamed.Name = "Renamed twice"
	renamed.UpdatedAt, renamed.UpdatedBy = itNow.Add(2*time.Minute), by
	_, err = repo.SaveDraftIdentity(ctx, renamed, current.UpdatedAt, uuid.Must(uuid.NewV7()), base.UpdatedAt)
	require.ErrorIs(t, err, pgx.ErrNoRows, "the second rename started from the revision the first one replaced")
}

func TestDraftRevisionRecordsWhoSavedLast(t *testing.T) {
	ctx, repo, ex, author := newDraftRevisionRepo(t)
	first := uuid.NullUUID{UUID: author, Valid: true}
	base, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("base"), itNow, first, time.Time{})
	require.NoError(t, err)
	require.Equal(t, first, base.CreatedBy)

	saved, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("again"), itNow, uuid.NullUUID{}, base.UpdatedAt)
	require.NoError(t, err)
	require.False(t, saved.UpdatedBy.Valid, "the last save carries the last saver (here none), not the creator")
	require.Equal(t, first, saved.CreatedBy, "the creator of the working copy does not change")
}

// Key rotation re-encrypts the stored secrets of a working copy without changing what an editor sees: it says so for its
// transaction and the revision stays, so an editor in the middle of a session is not refused. Any other write moves it.
func TestDraftRevisionKeyRotationKeepsItAndAnyOtherWriteMovesIt(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	ex := mustCreateExercise(t, repo, "Rotation keeps the revision")
	base, err := repo.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), contentWithNote("base"), itNow, uuid.NullUUID{}, time.Time{})
	require.NoError(t, err)

	tx, err := db.Pool.Begin(ctx)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SELECT set_config('cybericebox.keep_revision', '1', true)`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE exercise_versions SET variants = '[]'::jsonb WHERE id = $1`, base.ID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit(ctx))
	after, err := repo.GetDraftRevision(ctx, ex.ID)
	require.NoError(t, err)
	require.True(t, base.UpdatedAt.Equal(after.UpdatedAt), "a rotation does not move the revision")

	_, err = db.Pool.Exec(ctx, `UPDATE exercise_versions SET admin_note = 'edited elsewhere' WHERE id = $1`, base.ID)
	require.NoError(t, err)
	moved, err := repo.GetDraftRevision(ctx, ex.ID)
	require.NoError(t, err)
	require.True(t, moved.UpdatedAt.After(base.UpdatedAt), "a write outside the rotation does")
}
