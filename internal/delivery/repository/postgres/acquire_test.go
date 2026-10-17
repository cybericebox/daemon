package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/pkg/overload"
)

// onePool is a pool of a single connection on its own test database, so a test can hold the only connection.
func onePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	cfg := db.Pool.Config()
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// holdOnlyConn takes the only connection of the pool until the test ends.
func holdOnlyConn(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	conn, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Release)
}

func assertReleased(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if n := pool.Stat().AcquiredConns(); n != 0 {
		t.Fatalf("acquired connections = %d, want 0", n)
	}
}

func TestAcquireTimesOutWithDBBusy(t *testing.T) {
	pool := onePool(t)
	holdOnlyConn(t, pool)
	db := postgres.NewAcquireLimited(pool, 100*time.Millisecond)
	ctx := context.Background()

	calls := map[string]func() error{
		"exec": func() error { _, err := db.Exec(ctx, "SELECT 1"); return err },
		"query": func() error {
			rows, err := db.Query(ctx, "SELECT 1")
			if err == nil {
				rows.Close()
			}
			return err
		},
		"query row": func() error { var n int; return db.QueryRow(ctx, "SELECT 1").Scan(&n) },
		"begin": func() error {
			tx, err := db.Begin(ctx)
			if err == nil {
				_ = tx.Rollback(ctx)
			}
			return err
		},
		"begin tx": func() error {
			tx, err := db.BeginTx(ctx, pgx.TxOptions{})
			if err == nil {
				_ = tx.Rollback(ctx)
			}
			return err
		},
	}
	for name, call := range calls {
		start := time.Now()
		err := call()
		elapsed := time.Since(start)
		if !errors.Is(err, overload.ErrDBBusy) {
			t.Errorf("%s: err = %v, want ErrDBBusy", name, err)
		}
		if elapsed < 90*time.Millisecond || elapsed > time.Second {
			t.Errorf("%s: gave up after %v, want about 100ms", name, elapsed)
		}
	}
}

func TestAcquireDeadlineDoesNotCutRunningQuery(t *testing.T) {
	pool := onePool(t)
	db := postgres.NewAcquireLimited(pool, 100*time.Millisecond)
	ctx := context.Background()

	if _, err := db.Exec(ctx, "SELECT pg_sleep(0.3)"); err != nil {
		t.Fatalf("exec: %v", err)
	}
	var n int
	if err := db.QueryRow(ctx, "SELECT 1 FROM pg_sleep(0.3)").Scan(&n); err != nil {
		t.Fatalf("query row: %v", err)
	}
	rows, err := db.Query(ctx, "SELECT g FROM generate_series(1, 2) g, pg_sleep(0.3)")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	got, err := pgx.CollectRows(rows, pgx.RowTo[int])
	if err != nil || len(got) != 2 {
		t.Fatalf("query rows = %v, %v", got, err)
	}
	assertReleased(t, pool)
}

func TestCallerCancelNotDBBusy(t *testing.T) {
	pool := onePool(t)
	holdOnlyConn(t, pool)
	db := postgres.NewAcquireLimited(pool, time.Second)

	cancelled, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)
	_, err := db.Exec(cancelled, "SELECT 1")
	if errors.Is(err, overload.ErrDBBusy) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller: err = %v, want context.Canceled and not ErrDBBusy", err)
	}

	// A caller deadline shorter than the acquire timeout is the caller's own deadline, not a busy pool.
	expiring, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	_, err = db.Exec(expiring, "SELECT 1")
	if errors.Is(err, overload.ErrDBBusy) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expired caller: err = %v, want context.DeadlineExceeded and not ErrDBBusy", err)
	}
}

func TestQueryRowReleasesAfterScan(t *testing.T) {
	pool := onePool(t)
	db := postgres.NewAcquireLimited(pool, 100*time.Millisecond)
	ctx := context.Background()

	var n int
	if err := db.QueryRow(ctx, "SELECT 7").Scan(&n); err != nil || n != 7 {
		t.Fatalf("scan = %d, %v", n, err)
	}
	assertReleased(t, pool)
	// A statement error is reported by Scan and releases too.
	if err := db.QueryRow(ctx, "SELECT 1/0").Scan(&n); err == nil {
		t.Fatal("division by zero must fail")
	}
	assertReleased(t, pool)
}

func TestRowsCloseReleases(t *testing.T) {
	pool := onePool(t)
	db := postgres.NewAcquireLimited(pool, 100*time.Millisecond)
	ctx := context.Background()

	rows, err := db.Query(ctx, "SELECT g FROM generate_series(1, 3) g")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("no first row")
	}
	rows.Close()
	rows.Close() // a second Close is harmless
	assertReleased(t, pool)

	// Reading to the end releases even before Close.
	rows, err = db.Query(ctx, "SELECT g FROM generate_series(1, 3) g")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	assertReleased(t, pool)
	rows.Close()
	assertReleased(t, pool)

	// A statement that fails leaves nothing acquired.
	rows, err = db.Query(ctx, "SELEC 1")
	if err == nil {
		rows.Close()
		err = rows.Err()
	}
	if err == nil {
		t.Fatal("syntax error must fail")
	}
	assertReleased(t, pool)
}

func TestTxReleasesOnCommitAndRollback(t *testing.T) {
	pool := onePool(t)
	db := postgres.NewAcquireLimited(pool, 100*time.Millisecond)
	ctx := context.Background()

	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, "CREATE TEMP TABLE acquire_probe (n int)"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	assertReleased(t, pool)
	// The deferred Rollback of a committed unit of work must not release twice.
	if err = tx.Rollback(ctx); !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("rollback after commit = %v, want ErrTxClosed", err)
	}
	assertReleased(t, pool)

	tx, err = db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	assertReleased(t, pool)

	// A failed commit (the transaction aborted) still gives the connection back.
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tx.Exec(ctx, "SELECT 1/0")
	_ = tx.Commit(ctx)
	assertReleased(t, pool)
}
