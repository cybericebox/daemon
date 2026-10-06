package postgres

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
)

const migrationTable = "ap_backend_schema_migrations"

type (
	// PostgresRepository is the concrete postgres data store. It embeds the
	// pool-bound *Queries (so it IS a Querier for non-tx reads/writes) and holds
	// the tx-aware *DB used to mint per-useCase Units of Work via UoWFactory.
	PostgresRepository struct {
		*Queries
		db   *DB
		pool *pgxpool.Pool
		cfg  *config.PostgresConfig
	}

	Dependencies struct {
		Config *config.PostgresConfig
	}
)

func NewRepository(deps Dependencies) *PostgresRepository {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, deps.Config.DSN())
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open postgres pool")
	}
	if err = pool.Ping(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to ping postgres")
	}

	db := NewDB(pool)

	return &PostgresRepository{
		Queries: New(pool),
		db:      db,
		pool:    pool,
		cfg:     deps.Config,
	}
}

// Close releases the connection pool. pgxpool.Close blocks until EVERY connection
// is returned; a connection still held by a goroutine that outlived its owner
// (e.g. River after a timed-out stop) would hang the process forever on shutdown.
// Bound it so Ctrl+C always exits promptly — a connection still open at process
// exit is reclaimed by the OS anyway.
func (r *PostgresRepository) Close() {
	done := make(chan struct{})
	go func() {
		r.pool.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Warn().Msg("Postgres pool close timed out; forcing exit (a connection was not released)")
	}
}

// Pool exposes the underlying pool for infrastructure that needs it (e.g. River).
func (r *PostgresRepository) Pool() *pgxpool.Pool {
	return r.pool
}

// UoWFactory hands out the Unit of Work factory with the *DB already wired in.
// useCases mint their own typed workers from it (postgres.BuildUnitOfWorker),
// so the db itself never crosses into the business layer.
func (r *PostgresRepository) UoWFactory() *UoWFactory {
	return newUoWFactory(r.db)
}

// Migrate applies the schema migrations. It is a separate step — the caller
// creates the repository and decides when (or whether) to migrate, rather than
// pulling golang-migrate directly in the app bootstrap.
func (r *PostgresRepository) Migrate() error {
	db := stdlib.OpenDBFromPool(r.pool)
	defer func() { _ = db.Close() }()

	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{MigrationsTable: migrationTable})
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to init migrate driver").Err()
	}

	m, err := migrate.NewWithDatabaseInstance("file://"+r.cfg.MigrationsPath, "postgres", driver)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to init migrator").Err()
	}

	if err = m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to run migrations").Err()
	}

	return nil
}

// SchemaCurrent reports an error unless the database is at the newest migration of the migrations
// directory (and not half-applied). Unlike Migrate it changes nothing: the one-off commands that must
// not migrate use it to refuse to run against an old schema.
func (r *PostgresRepository) SchemaCurrent() error {
	entries, err := os.ReadDir(r.cfg.MigrationsPath)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the migrations directory").Err()
	}
	var latest uint64
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".up.sql") {
			continue
		}
		version, _, _ := strings.Cut(name, "_")
		n, perr := strconv.ParseUint(version, 10, 64)
		if perr != nil {
			continue
		}
		latest = max(latest, n)
	}

	db := stdlib.OpenDBFromPool(r.pool)
	defer func() { _ = db.Close() }()
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{MigrationsTable: migrationTable})
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to init migrate driver").Err()
	}
	current, dirty, err := driver.Version()
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read the schema version").Err()
	}
	if dirty || current < 0 || uint64(current) != latest {
		return model.ErrPlatform.WithError(fmt.Errorf("schema version %d (dirty: %t), expected %d", current, dirty, latest)).
			WithMessage("The database schema is not current: start the backend first so it migrates").Err()
	}
	return nil
}
