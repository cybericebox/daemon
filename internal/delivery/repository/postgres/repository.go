package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/golang-migrate/migrate/v4"
	pg "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
)

const migrationTable = "daemon_schema_migrations"

type (
	PostgresRepository struct {
		*Queries
		db *pgxpool.Pool
	}

	Dependencies struct {
		Config *config.PostgresConfig
	}
)

func NewRepository(deps Dependencies) *PostgresRepository {
	ctx := context.Background()
	db, err := newPostgresDB(ctx, deps.Config)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create new postgres db connection")
	}

	if err = runMigrations(deps.Config); err != nil {
		log.Fatal().Err(err).Msg("Failed to run db migrations")
	}

	return &PostgresRepository{
		Queries: New(db),
		db:      db,
	}
}

func newPostgresDB(ctx context.Context, cfg *config.PostgresConfig) (*pgxpool.Pool, error) {
	ConnConfig, err := pgxpool.ParseConfig(
		fmt.Sprintf("user=%s password=%s dbname=%s host=%s port=%d sslmode=%s", cfg.Username, cfg.Password, cfg.Database, cfg.Host, cfg.Port, cfg.SSLMode))
	conn, err := pgxpool.NewWithConfig(ctx, ConnConfig)
	if err != nil {
		return nil, model.ErrPostgres.WithError(err).WithMessage("Failed to create new postgres db connection").Cause()
	}

	// ping db
	if err = conn.Ping(ctx); err != nil {
		return nil, model.ErrPostgres.WithError(err).WithMessage("Failed to ping db").Cause()
	}

	return conn, nil

}

func runMigrations(cfg *config.PostgresConfig) error {
	db, err := sql.Open("postgres", fmt.Sprintf("user=%s password=%s dbname=%s host=%s port=%d sslmode=%s", cfg.Username, cfg.Password, cfg.Database, cfg.Host, cfg.Port, cfg.SSLMode))
	if err != nil {
		return model.ErrPostgres.WithError(err).WithMessage("Failed to open db connection").Cause()
	}
	defer func() {
		if err = db.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close db connection after running migrations")
		}
	}()
	driver, err := pg.WithInstance(db, &pg.Config{
		MigrationsTable: migrationTable,
		DatabaseName:    cfg.Database,
	})
	if err != nil {
		return model.ErrPostgres.WithError(err).WithMessage("Failed to create migration driver").Cause()
	}

	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", config.MigrationPath),
		cfg.Database,
		driver,
	)

	if err != nil {
		return model.ErrPostgres.WithError(err).WithMessage("Failed to create migration instance").Cause()
	}

	if err = m.Up(); err != nil {
		if !errors.Is(migrate.ErrNoChange, err) {
			return model.ErrPostgres.WithError(err).WithMessage("Failed to run migrations").Cause()
		}
	}
	return nil
}

func (r *PostgresRepository) GetSQLDB() *pgxpool.Pool {
	return r.db
}

func (r *PostgresRepository) WithTransaction(ctx context.Context) (withTx Querier, commit func(), rollback func(), err error) {
	tx, err := r.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return nil, nil, nil, model.ErrPostgres.WithError(err).WithMessage("Failed to begin transaction").Cause()
	}

	withTx = r.WithTx(tx)

	rollback = func() {
		if err = tx.Rollback(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to rollback transaction")
		}
	}

	commit = func() {
		if err = tx.Commit(ctx); err != nil {
			log.Error().Err(err).Msg("Failed to commit transaction")
			rollback()
		}
	}

	return withTx, commit, rollback, nil
}
