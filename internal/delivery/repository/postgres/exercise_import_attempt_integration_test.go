package postgres_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLegacyImportOutcomeWriterCannotOverwriteCompletedImport(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := mustSeedUser(t, db, "import-outcome@test.test")
	id, source, task := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	_, err := db.Queries.StartExerciseImportJob(ctx, postgres.StartExerciseImportJobParams{ID: id, CreatedBy: user, Source: "upload", SourceID: source, State: "running", ExerciseIds: []uuid.UUID{}, CreatedAt: itNow, UpdatedAt: itNow})
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `UPDATE exercise_import_jobs SET state='done',exercise_ids=ARRAY[$2::uuid] WHERE id=$1`, id, task)
	require.NoError(t, err)
	config := db.Pool.Config()
	delete(config.ConnConfig.RuntimeParams, postgres.CatalogWriterCapability)
	legacy, err := pgxpool.NewWithConfig(ctx, config)
	require.NoError(t, err)
	defer legacy.Close()
	_, insertErr := legacy.Exec(ctx, `INSERT INTO exercise_import_jobs(id,created_by,source,source_id,state,exercise_ids,created_at,updated_at) VALUES($1,$2,'upload',$3,'running','{}',now(),now())`, uuid.Must(uuid.NewV7()), user, uuid.Must(uuid.NewV7()))
	var refusedInsert *pgconn.PgError
	require.ErrorAs(t, insertErr, &refusedInsert)
	require.Equal(t, "40001", refusedInsert.Code)
	// Exact UPDATE used by the old repository outcome writer: id alone, no state/generation fence.
	_, err = legacy.Exec(ctx, `UPDATE exercise_import_jobs SET state='failed',exercise_ids='{}',error='{"Code":70000,"Message":"old worker failed"}' WHERE id=$1`, id)
	var refused *pgconn.PgError
	require.ErrorAs(t, err, &refused, "an overlapping old worker must not erase a new durable outcome")
	require.Equal(t, "40001", refused.Code)
	row, err := db.Queries.GetExerciseImportJob(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "done", row.State)
	require.Equal(t, []uuid.UUID{task}, row.ExerciseIds)
	_, deleteErr := legacy.Exec(ctx, `DELETE FROM exercise_import_jobs WHERE id=$1`, id)
	var refusedDelete *pgconn.PgError
	require.ErrorAs(t, deleteErr, &refusedDelete)
	require.Equal(t, "40001", refusedDelete.Code)
	_, err = db.Queries.GetExerciseImportJob(ctx, id)
	require.NoError(t, err)
}

func TestImportAttemptCASPreservesRestartAndDurableOutcome(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := mustSeedUser(t, db, "import-cas@test.test")
	id, source, task := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	row, err := db.Queries.StartExerciseImportJob(ctx, postgres.StartExerciseImportJobParams{ID: id, CreatedBy: user, Source: "upload", SourceID: source, State: "running", ExerciseIds: []uuid.UUID{}, CreatedAt: itNow, UpdatedAt: itNow})
	require.NoError(t, err)
	require.EqualValues(t, 1, row.Generation)
	finish := postgres.UpdateExerciseImportJobParams{ID: id, Generation: 1, State: "failed", ExerciseIds: []uuid.UUID{}, Results: []byte("[]"), Stage: "failed", UpdatedAt: itNow}
	n, err := db.Queries.UpdateExerciseImportJob(ctx, finish)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	restart := postgres.RestartExerciseImportJobParams{ID: id, ExpectedGeneration: 1, Options: []byte("{}"), PlanHash: "new-plan", UpdatedAt: itNow.Add(time.Minute), StaleBefore: itNow.Add(-time.Hour)}
	row, err = db.Queries.RestartExerciseImportJob(ctx, restart)
	require.NoError(t, err)
	require.EqualValues(t, 2, row.Generation)
	progress := postgres.UpdateExerciseImportJobProgressParams{ID: id, Generation: 1, Stage: "images", ImagesDone: 9, UpdatedAt: itNow.Add(2 * time.Minute)}
	n, err = db.Queries.UpdateExerciseImportJobProgress(ctx, progress)
	require.NoError(t, err)
	require.Zero(t, n)
	for _, state := range []string{"failed", "done"} {
		finish.State = state
		n, err = db.Queries.UpdateExerciseImportJob(ctx, finish)
		require.NoError(t, err)
		require.Zero(t, n, "stale %s must not mutate attempt2", state)
	}
	_, err = db.Queries.RestartExerciseImportJob(ctx, restart)
	require.Error(t, err, "a stale restart cannot advance a newer attempt")
	progress.Generation = 2
	n, err = db.Queries.UpdateExerciseImportJobProgress(ctx, progress)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	finish.Generation = 2
	finish.State = "done"
	finish.Stage = "done"
	finish.ExerciseIds = []uuid.UUID{task}
	finish.Results = []byte(`[{"ItemKey":".","Mode":"update","ExerciseID":"` + task.String() + `"}]`)
	n, err = db.Queries.UpdateExerciseImportJob(ctx, finish)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = db.Queries.UpdateExerciseImportJobProgress(ctx, progress)
	require.NoError(t, err)
	require.Zero(t, n)
	finish.State = "failed"
	finish.ExerciseIds = []uuid.UUID{}
	finish.Results = []byte("[]")
	n, err = db.Queries.UpdateExerciseImportJob(ctx, finish)
	require.NoError(t, err)
	require.Zero(t, n, "same-generation failure must not erase done")
	row, err = db.Queries.GetExerciseImportJob(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "done", row.State)
	require.Equal(t, []uuid.UUID{task}, row.ExerciseIds)
	require.Contains(t, string(row.Results), "update")
}
