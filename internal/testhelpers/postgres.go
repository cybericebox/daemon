// Package testhelpers provides shared fixtures for integration tests.
package testhelpers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// TestDB is a disposable PostgreSQL instance with the real schema applied —
// integration tests exercise the actual sqlc queries against it instead of
// mocks.
type TestDB struct {
	Pool    *pgxpool.Pool
	Queries *postgres.Queries
}

// SetupTestDB starts a PostgreSQL container, applies the repository's
// golang-migrate migrations, and returns live Queries. The container and pool
// are cleaned up via t.Cleanup. When Docker is not available the test is
// skipped, so the suite stays runnable on machines without it.
func SetupTestDB(t *testing.T) *TestDB {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("testuser"),
		tcpostgres.WithPassword("testpass"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		t.Skipf("skipping integration test: cannot start postgres container (docker running?): %v", err)
	}
	t.Cleanup(func() {
		if terr := testcontainers.TerminateContainer(container); terr != nil {
			t.Logf("failed to terminate postgres container: %v", terr)
		}
	})

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(pool.Close)

	applyMigrations(t, pool)

	return &TestDB{Pool: pool, Queries: postgres.New(pool)}
}

// applyMigrations runs the repository's real migration files against the pool.
func applyMigrations(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		t.Fatalf("init migrate driver: %v", err)
	}

	migrationsPath := filepath.Join(repoRoot(t), "internal", "delivery", "repository", "postgres", "migrations")
	m, err := migrate.NewWithDatabaseInstance("file://"+migrationsPath, "postgres", driver)
	if err != nil {
		t.Fatalf("init migrator: %v", err)
	}
	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		t.Fatalf("apply migrations: %v", err)
	}
	// Release the connection the migrate driver holds — otherwise pool.Close
	// in the test cleanup blocks forever waiting for it.
	if serr, derr := m.Close(); serr != nil || derr != nil {
		t.Fatalf("close migrator: source=%v db=%v", serr, derr)
	}
}

// repoRoot walks up from the working directory to the module root (go.mod),
// so tests in any package resolve the migrations directory the same way.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above working directory")
		}
		dir = parent
	}
}
