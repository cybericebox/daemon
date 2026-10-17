package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

const (
	itFilesHashA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	itFilesHashB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func mustCreateFile(t *testing.T, db *testhelpers.TestDB, id uuid.UUID, hash string, at time.Time) {
	t.Helper()
	_, err := db.Queries.CreateFile(context.Background(), postgres.CreateFileParams{
		ID: id, Name: "f-" + id.String()[len(id.String())-4:], ContentType: "text/plain", SizeBytes: 3, ContentHash: hash, CreatedAt: at,
	})
	require.NoError(t, err)
}

// Two uploads of one content into one task: the second claim returns the first file; another task keeps its own.
func TestExerciseFiles_ClaimReturnsCanonical(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	taskOne, taskTwo := mustCreateExercise(t, repo, "Files one"), mustCreateExercise(t, repo, "Files two")
	first := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000a0001")
	second := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000a0002")
	third := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000a0003")
	for _, id := range []uuid.UUID{first, second, third} {
		mustCreateFile(t, db, id, itFilesHashA, itNow)
	}

	claim := func(task, file uuid.UUID) uuid.UUID {
		got, err := db.Queries.ClaimExerciseFile(ctx, postgres.ClaimExerciseFileParams{ExerciseID: task, ContentHash: itFilesHashA, FileID: file, CreatedAt: itNow})
		require.NoError(t, err)
		return got
	}
	require.Equal(t, first, claim(taskOne.ID, first))
	require.Equal(t, first, claim(taskOne.ID, second))
	require.Equal(t, third, claim(taskTwo.ID, third))

	found, err := db.Queries.GetExerciseFileByHash(ctx, postgres.GetExerciseFileByHashParams{ExerciseID: taskOne.ID, ContentHash: itFilesHashA})
	require.NoError(t, err)
	require.Equal(t, first, found.ID)
	require.EqualValues(t, 3, found.SizeBytes)
	_, err = db.Queries.GetExerciseFileByHash(ctx, postgres.GetExerciseFileByHashParams{ExerciseID: taskOne.ID, ContentHash: itFilesHashB})
	require.Error(t, err)

	holds, err := db.Queries.ExerciseHoldsFile(ctx, postgres.ExerciseHoldsFileParams{ExerciseID: taskOne.ID, FileID: first})
	require.NoError(t, err)
	require.True(t, holds)
	// Another task's file is never held by this one.
	holds, err = db.Queries.ExerciseHoldsFile(ctx, postgres.ExerciseHoldsFileParams{ExerciseID: taskOne.ID, FileID: third})
	require.NoError(t, err)
	require.False(t, holds)

	hashes, err := db.Queries.ListFileHashes(ctx, []uuid.UUID{first, third})
	require.NoError(t, err)
	require.Len(t, hashes, 2)
	for _, row := range hashes {
		require.Equal(t, itFilesHashA, row.ContentHash)
	}
}

// The media GC removes an unreferenced file past the grace window; the task's row goes with it, and a file a
// version still references stays.
func TestExerciseFiles_CascadeOnFileDelete(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	task := mustCreateExercise(t, repo, "Files cascade")
	old := itNow.Add(-48 * time.Hour)
	dropped := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000b0001")
	kept := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000b0002")
	mustCreateFile(t, db, dropped, itFilesHashA, old)
	mustCreateFile(t, db, kept, itFilesHashB, old)
	for file, hash := range map[uuid.UUID]string{dropped: itFilesHashA, kept: itFilesHashB} {
		_, err := db.Queries.ClaimExerciseFile(ctx, postgres.ClaimExerciseFileParams{ExerciseID: task.ID, ContentHash: hash, FileID: file, CreatedAt: old})
		require.NoError(t, err)
	}
	// An older version still links the kept file.
	require.NoError(t, db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{
		RefType: "exercise_version", RefID: uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000b0099"), FileIds: []uuid.UUID{kept},
	}))

	_, err := db.Queries.DeleteUnreferencedFiles(ctx, itNow.Add(-24*time.Hour))
	require.NoError(t, err)

	_, err = db.Queries.GetExerciseFileByHash(ctx, postgres.GetExerciseFileByHashParams{ExerciseID: task.ID, ContentHash: itFilesHashA})
	require.Error(t, err, "the row of a deleted file must go by cascade")
	found, err := db.Queries.GetExerciseFileByHash(ctx, postgres.GetExerciseFileByHashParams{ExerciseID: task.ID, ContentHash: itFilesHashB})
	require.NoError(t, err)
	require.Equal(t, kept, found.ID)
}

// The migration's backfill keeps one row per (task, hash): the earliest file the task's versions reference.
func TestExerciseFiles_Backfill(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	taskOne, taskTwo := mustCreateExercise(t, repo, "Backfill one"), mustCreateExercise(t, repo, "Backfill two")
	versionOne, err := repo.UpsertDraft(ctx, taskOne.ID, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000c0101"), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	require.NoError(t, err)
	versionTwo, err := repo.UpsertDraft(ctx, taskTwo.ID, uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000c0102"), exerciseModel.ExerciseVersion{Variants: itVariants()}, itNow, uuid.NullUUID{})
	require.NoError(t, err)

	earliest := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000c0002")
	later := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000c0001")
	other := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000c0003")
	otherTask := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000c0004")
	unlinked := uuid.FromStringOrNil("0192f0a0-0000-7000-8000-0000000c0005")
	mustCreateFile(t, db, earliest, itFilesHashA, itNow.Add(-2*time.Hour))
	mustCreateFile(t, db, later, itFilesHashA, itNow.Add(-time.Hour))
	mustCreateFile(t, db, other, itFilesHashB, itNow)
	mustCreateFile(t, db, otherTask, itFilesHashA, itNow)
	mustCreateFile(t, db, unlinked, itFilesHashB, itNow)
	link := func(version uuid.UUID, files ...uuid.UUID) {
		require.NoError(t, db.Queries.ReplaceFileReferences(ctx, postgres.ReplaceFileReferencesParams{RefType: "exercise_version", RefID: version, FileIds: files}))
	}
	link(versionOne.ID, later, earliest, other)
	link(versionTwo.ID, otherTask)

	up, err := os.ReadFile("migrations/0175_exercise_files.up.sql")
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, "DROP TABLE exercise_files")
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(up))
	require.NoError(t, err)

	rows, err := db.Pool.Query(ctx, "SELECT exercise_id, content_hash, file_id FROM exercise_files ORDER BY exercise_id, content_hash")
	require.NoError(t, err)
	type row struct {
		Task uuid.UUID
		Hash string
		File uuid.UUID
	}
	var got []row
	for rows.Next() {
		var r row
		require.NoError(t, rows.Scan(&r.Task, &r.Hash, &r.File))
		got = append(got, r)
	}
	require.NoError(t, rows.Err())
	want := []row{
		{taskOne.ID, itFilesHashA, earliest},
		{taskOne.ID, itFilesHashB, other},
		{taskTwo.ID, itFilesHashA, otherTask},
	}
	if taskTwo.ID.String() < taskOne.ID.String() {
		want = []row{want[2], want[0], want[1]}
	}
	require.Equal(t, want, got)
}
