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

// Open imports are the running and failed ones the user has not closed; a change of the job shows a closed one again.
func TestOpenImportJobsAndDismissal(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := mustSeedUser(t, db, "import-open@test.test")
	running, failed, done := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for _, c := range []struct {
		id    uuid.UUID
		state string
	}{{running, "running"}, {failed, "failed"}, {done, "done"}} {
		_, err := db.Queries.StartExerciseImportJob(ctx, postgres.StartExerciseImportJobParams{ID: c.id, CreatedBy: user, Source: "upload", SourceID: uuid.Must(uuid.NewV7()), State: c.state, ExerciseIds: []uuid.UUID{}, CreatedAt: itNow, UpdatedAt: itNow})
		require.NoError(t, err)
	}
	ids := func() []uuid.UUID {
		rows, err := db.Queries.ListOpenExerciseImportJobs(ctx, user)
		require.NoError(t, err)
		out := make([]uuid.UUID, len(rows))
		for i, r := range rows {
			out[i] = r.ID
		}
		return out
	}
	require.ElementsMatch(t, []uuid.UUID{running, failed}, ids(), "a done import is not open")
	stranger := mustSeedUser(t, db, "import-stranger@test.test")
	n, err := db.Queries.DismissExerciseImportJob(ctx, postgres.DismissExerciseImportJobParams{JobID: failed, CreatedBy: stranger, DismissedAt: itNow.Add(time.Minute)})
	require.NoError(t, err)
	require.Zero(t, n, "another user's import is not closed")
	require.ElementsMatch(t, []uuid.UUID{running, failed}, ids())
	var rows int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM exercise_import_dismissals`).Scan(&rows))
	require.Zero(t, rows, "nothing written")
	n, err = db.Queries.DismissExerciseImportJob(ctx, postgres.DismissExerciseImportJobParams{JobID: failed, CreatedBy: user, DismissedAt: itNow.Add(time.Minute)})
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.Equal(t, []uuid.UUID{running}, ids())
	// the job changes after the dismissal (a rerun): it is listed again
	_, err = db.Queries.RestartExerciseImportJob(ctx, postgres.RestartExerciseImportJobParams{ID: failed, ExpectedGeneration: 1, Options: []byte("{}"), UpdatedAt: itNow.Add(2 * time.Minute), StaleBefore: itNow})
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{running, failed}, ids())
}

// An account has at most one identity of a provider, whatever the interleaving of the links.
func TestUserProviderIsUniquePerAccountAndKind(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := mustSeedUser(t, db, "one-google@test.test")
	link := func(providerUserID string) error {
		_, err := db.Queries.CreateUserProvider(ctx, postgres.CreateUserProviderParams{ID: uuid.Must(uuid.NewV7()), UserID: user, Provider: "google", ProviderUserID: providerUserID})
		return err
	}
	require.NoError(t, link("g-1"))
	var refused *pgconn.PgError
	require.ErrorAs(t, link("g-2"), &refused, "a second Google identity for the account")
	require.Equal(t, "23505", refused.Code)
	_, err := db.Queries.CreateUserProvider(ctx, postgres.CreateUserProviderParams{ID: uuid.Must(uuid.NewV7()), UserID: user, Provider: "github", ProviderUserID: "h-1"})
	require.NoError(t, err, "another provider is not affected")
}
