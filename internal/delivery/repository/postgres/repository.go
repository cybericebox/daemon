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
	// acquire-limited *Queries (so it IS a Querier for non-tx reads/writes) and holds
	// the tx-aware *DB used to mint per-useCase Units of Work via UoWFactory.
	PostgresRepository struct {
		*Queries
		db      *DB
		pool    *pgxpool.Pool
		limited *AcquireLimited
		jobPool *pgxpool.Pool
		cfg     *config.PostgresConfig
	}

	Dependencies struct {
		Config *config.PostgresConfig
	}
)

// newPoolConfig sizes one pool of the replica. pgx would default to the CPU count (4 on a 4-vCPU node). The main
// pool (POSTGRES_MAX_CONNS, 32) serves requests and use cases, job handlers included: their concurrency is bounded
// by the job workers (default 6 + durable notices 2 + images 2). The job pool (POSTGRES_JOB_MAX_CONNS, 8) carries
// River itself (fetching, LISTEN, maintenance, migrations) and the session revocation poll, so a flood of requests
// cannot starve the poll into mass 503s, and jobs holding a transaction cannot starve requests. The server allows 200.
func newPoolConfig(dsn string, maxConns int, cfg *config.PostgresConfig) (*pgxpool.Config, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	poolConfig.MaxConns = int32(maxConns) //nolint:gosec // validated: at least 4, far below int32
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime
	return poolConfig, nil
}

func openPool(
	ctx context.Context,
	dsn string,
	maxConns int,
	cfg *config.PostgresConfig,
) *pgxpool.Pool {
	poolConfig, err := newPoolConfig(dsn, maxConns, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to parse postgres pool config")
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to open postgres pool")
	}
	if err = pool.Ping(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to ping postgres")
	}
	return pool
}

func NewRepository(deps Dependencies) *PostgresRepository {
	ctx := context.Background()

	pool := openPool(ctx, deps.Config.DSN(), deps.Config.MaxConns, deps.Config)
	jobPool := openPool(ctx, deps.Config.DSN(), deps.Config.JobMaxConns, deps.Config)
	limited := NewAcquireLimited(pool, deps.Config.AcquireTimeout)

	return &PostgresRepository{
		Queries: New(limited),
		db:      NewDB(limited),
		pool:    pool,
		limited: limited,
		jobPool: jobPool,
		cfg:     deps.Config,
	}
}

// Close releases both connection pools. pgxpool.Close blocks until EVERY connection
// is returned; a connection still held by a goroutine that outlived its owner
// (e.g. River after a timed-out stop) would hang the process forever on shutdown.
// Bound it so Ctrl+C always exits promptly — a connection still open at process
// exit is reclaimed by the OS anyway.
func (r *PostgresRepository) Close() {
	done := make(chan struct{})
	go func() {
		r.jobPool.Close()
		r.pool.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		log.Warn().
			Msg("Postgres pool close timed out; forcing exit (a connection was not released)")
	}
}

// Pool exposes the raw main pool, with no acquire timeout, for infrastructure that holds a connection of its own
// (the laboratory monitoring lock, the seed command, migrations).
func (r *PostgresRepository) Pool() *pgxpool.Pool {
	return r.pool
}

// Limited is the main pool with the bounded wait for a connection, for the repositories that run raw statements
// next to sqlc (Exec, QueryRow, Begin).
func (r *PostgresRepository) Limited() *AcquireLimited {
	return r.limited
}

// JobPool is the separate pool of the job queue (River) and the session revocation poll.
func (r *PostgresRepository) JobPool() *pgxpool.Pool {
	return r.jobPool
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
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to read the migrations directory").
			Err()
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
		return model.ErrPlatform.WithError(err).
			WithMessage("Failed to read the schema version").
			Err()
	}
	if dirty || current < 0 || uint64(current) != latest {
		return model.ErrPlatform.WithError(fmt.Errorf("schema version %d (dirty: %t), expected %d", current, dirty, latest)).
			WithMessage("The database schema is not current: start the backend first so it migrates").
			Err()
	}
	return nil
}
