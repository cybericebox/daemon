package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/pkg/overload"
)

type acquireProbeArgs struct{}

func (acquireProbeArgs) Kind() string { return "acquire_probe" }

// insertOnlyRiver migrates the River tables on pool and returns an insert-only client on it.
func insertOnlyRiver(t *testing.T, pool *pgxpool.Pool) *river.Client[pgx.Tx] {
	t.Helper()
	driver := riverpgxv5.New(pool)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil); err != nil {
		t.Fatal(err)
	}
	jobs, err := river.NewClient[pgx.Tx](driver, &river.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return jobs
}

func countProbeJobs(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	err := pool.QueryRow(context.Background(), `SELECT count(*) FROM river_job WHERE kind = 'acquire_probe'`).
		Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// A unit of work over the acquire-limited pool holds its one connection until Save/Restore, and a job inserted
// by a River client on a separate pool joins that transaction: rolled back with it, committed with it.
func TestUnitOfWorkOverAcquireLimitedJoinsRiverInsert(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()

	cfg := db.Pool.Config()
	cfg.MaxConns = 1
	mainPool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer mainPool.Close()
	limited := postgres.NewAcquireLimited(mainPool, 100*time.Millisecond)
	// River on its own pool (the job pool).
	jobs := insertOnlyRiver(t, db.Pool)
	worker := postgres.NewUnitOfWorker[postgres.Querier](postgres.NewUoWFactory(limited))

	for want, commit := range []bool{false, true} {
		txCtx, _, uow, uerr := worker.UnitOfWork(ctx)
		if uerr != nil {
			t.Fatal(uerr)
		}
		tx, _ := postgres.TransactionFromContext(txCtx)
		if _, err = jobs.InsertTx(txCtx, tx, acquireProbeArgs{}, nil); err != nil {
			t.Fatal(err)
		}
		// The unit of work holds the only connection: a statement outside it is refused as busy.
		if _, err = limited.Exec(ctx, "SELECT 1"); !errors.Is(err, overload.ErrDBBusy) {
			t.Fatalf("statement beside an open unit of work: err = %v, want ErrDBBusy", err)
		}
		end := uow.Restore
		if commit {
			end = uow.Save
		}
		if err = end(); err != nil {
			t.Fatal(err)
		}
		_ = uow.Restore()
		if n := mainPool.Stat().AcquiredConns(); n != 0 {
			t.Fatalf("acquired connections after the unit of work = %d, want 0", n)
		}
		// Rolled back: still none; committed: the one job.
		if got := countProbeJobs(t, db.Pool); got != want {
			t.Fatalf("commit=%t: jobs = %d, want %d", commit, got, want)
		}
	}
}
