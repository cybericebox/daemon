// Package testhelpers provides shared fixtures for integration tests.
package testhelpers

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
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

// sharedServer is one PostgreSQL container per test binary, holding a template database with the real schema
// applied once. Every test gets its own database cloned from the template (a file-level copy, a fraction of a
// second) instead of its own container and a full migration run: the postgres integration package has hundreds of
// tests and would not finish in the default test timeout otherwise.
var sharedServer struct {
	once      sync.Once
	container *tcpostgres.PostgresContainer
	baseDSN   string // admin connection to the maintenance database
	err       error
	next      atomic.Int64
}

const templateDB = "cib_template"

func startSharedServer() {
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
		sharedServer.err = err
		return
	}
	sharedServer.container = container
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		sharedServer.err = err
		return
	}
	sharedServer.baseDSN = dsn
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		sharedServer.err = err
		return
	}
	defer admin.Close()
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+templateDB); err != nil {
		sharedServer.err = err
		return
	}
	pool, err := pgxpool.New(ctx, dsnForDatabase(dsn, templateDB))
	if err != nil {
		sharedServer.err = err
		return
	}
	defer pool.Close()
	sharedServer.err = migrateUp(pool)
}

// dsnForDatabase is dsn with another database name.
func dsnForDatabase(dsn, name string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.Path = "/" + name
	return u.String()
}

// SetupTestDB gives the test its own PostgreSQL database with the real schema (cloned from a template inside one
// shared container, started by the first test of the binary) and returns live Queries. The database is dropped
// via t.Cleanup. When Docker is not available the test is skipped, so the suite stays runnable on machines
// without it.
func SetupTestDB(t *testing.T) *TestDB {
	t.Helper()
	ctx := context.Background()

	sharedServer.once.Do(startSharedServer)
	if sharedServer.err != nil {
		if sharedServer.container == nil {
			t.Skipf("skipping integration test: cannot start postgres container (docker running?): %v", sharedServer.err)
		}
		t.Fatalf("prepare the shared test database: %v", sharedServer.err)
	}

	admin, err := pgxpool.New(ctx, sharedServer.baseDSN)
	if err != nil {
		t.Fatalf("connect to the test server: %v", err)
	}
	defer admin.Close()
	name := fmt.Sprintf("cib_test_%d", sharedServer.next.Add(1))
	if _, err = admin.Exec(ctx, "CREATE DATABASE "+name+" TEMPLATE "+templateDB); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	pool, err := pgxpool.New(ctx, dsnForDatabase(sharedServer.baseDSN, name))
	if err != nil {
		t.Fatalf("connect to test database: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cerr := pgxpool.New(ctx, sharedServer.baseDSN)
		if cerr != nil {
			return
		}
		defer cleanup.Close()
		if _, derr := cleanup.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)"); derr != nil {
			t.Logf("failed to drop %s: %v", name, derr)
		}
	})

	return &TestDB{Pool: pool, Queries: postgres.New(pool)}
}

// migrateUp runs the repository's real migration files against the pool.
func migrateUp(pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()

	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		return fmt.Errorf("init migrate driver: %w", err)
	}
	root, err := repoRoot()
	if err != nil {
		return err
	}
	migrationsPath := filepath.Join(root, "internal", "delivery", "repository", "postgres", "migrations")
	m, err := migrate.NewWithDatabaseInstance("file://"+migrationsPath, "postgres", driver)
	if err != nil {
		return fmt.Errorf("init migrator: %w", err)
	}
	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations: %w", err)
	}
	// Release the connection the migrate driver holds — otherwise the template database cannot be cloned.
	if serr, derr := m.Close(); serr != nil || derr != nil {
		return fmt.Errorf("close migrator: source=%v db=%v", serr, derr)
	}
	return nil
}

// repoRoot walks up from the working directory to the module root (go.mod),
// so tests in any package resolve the migrations directory the same way.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("go.mod not found above working directory")
		}
		dir = parent
	}
}
