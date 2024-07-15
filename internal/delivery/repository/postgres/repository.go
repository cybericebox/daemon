package postgres

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/golang-migrate/migrate/v4"
	pg "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/rs/zerolog/log"
)

const migrationTable = "daemon_schema_migrations"

type (
	PostgresRepository struct {
		*Queries
		db *sqlx.DB
	}

	Dependencies struct {
		Config *config.PostgresConfig
	}
)

func NewRepository(deps Dependencies) *PostgresRepository {
	db, err := newPostgresDB(deps.Config)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create new postgres db connection")
	}

	if err = runMigrations(db, deps.Config.Database); err != nil {
		log.Fatal().Err(err).Msg("Failed to run db migrations")
	}

	if err = populateDefaultSettings(db); err != nil {
		log.Fatal().Err(err).Msg("Failed to populate default settings")
	}

	return &PostgresRepository{
		Queries: New(db),
		db:      db,
	}
}

func newPostgresDB(cfg *config.PostgresConfig) (*sqlx.DB, error) {
	db, err := sqlx.Connect("postgres", fmt.Sprintf("user=%s password=%s dbname=%s host=%s port=%s sslmode=%s",
		cfg.Username, cfg.Password, cfg.Database, cfg.Host, cfg.Port, cfg.SSLMode))
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to create new postgres db connection")
	}

	err = db.Ping()
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to ping postgres db")
	}

	return db, nil
}

func runMigrations(db *sqlx.DB, dbName string) error {
	driver, err := pg.WithInstance(db.DB, &pg.Config{
		MigrationsTable: migrationTable,
	})
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to create postgres driver")
	}

	m, err := migrate.NewWithDatabaseInstance(
		fmt.Sprintf("file://%s", config.MigrationPath),
		dbName,
		driver,
	)
	if err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to create migration instance")
	}

	if err = m.Up(); err != nil {
		if !errors.Is(migrate.ErrNoChange, err) {
			return appError.NewError().WithError(err).WithMessage("failed to run migrations")
		}
	}
	return nil
}

func populateDefaultSettings(db *sqlx.DB) error {
	if _, err := db.Exec("insert into platform_settings\n    (type, key, value) values\n('email_template_subject', 'account_exists_template', 'Спроба зареєструвати існуючий обліковий запис'),\n('email_template_body', 'account_exists_template', '<!DOCTYPE html>\n<html lang=\"uk\">\n<body>\n<h3>Вітаємо, {{.Username}}!</h3>\n<p>Цей лист було відправлено на запит про реєстрацію вже існуючого облікового запису</p>\n<p>Якщо виникла помилка, проігноруйте цей лист.</p>\n</body>\n</html>'\n),\n('email_template_subject', 'continue_registration_template', 'Продовження реєстрації'),\n('email_template_body', 'continue_registration_template', '<!DOCTYPE html>\n<html lang=\"uk\">\n<body>\n<h3>Вітаємо!</h3>\n<p>Цей лист було відправлено на запит про підтвердження адреси електронної пошти.</p>\n<p>Якщо виникла помилка, проігноруйте цей лист.</p>\n<p>Щоб підтвердити адресу електронної пошти перейдіть за наступним посиланням:</p><br/><span><a href=\"{{.Link}}\">{{.Link}}</a></span>\n</body>\n</html>'),\n('email_template_subject', 'email_confirmation_template', ' Підтвердження електронної пошти'),\n('email_template_body', 'email_confirmation_template', '<!DOCTYPE html>\n<html lang=\"uk\">\n<body>\n<h3>Вітаємо, {{.Username}}!</h3>\n<p>Цей лист було відправлено на запит про підтвердження адреси електронної пошти.</p>\n<p>Якщо виникла помилка, проігноруйте цей лист.</p>\n<p>Щоб підтвердити адресу електронної пошти перейдіть за наступним посиланням:</p><br/><span><a href=\"{{.Link}}\">{{.Link}}</a></span>\n</body>\n</html>'),\n('email_template_subject', 'password_resetting_template', 'Скидання пароля'),\n('email_template_body', 'password_resetting_template', '<!DOCTYPE html>\n<html lang=\"uk\">\n<body>\n<h3>Вітаємо, {{.Username}}!</h3>\n<p>Цей лист було відправлено на запит про відновлення паролю на пратформі Cyber ICE Box</p>\n<p>Якщо виникла помилка, проігноруйте цей лист.</p>\n<p>Щоб відновити пароль перейдіть за наступним посиланням:</p><br/><span><a href=\"{{.Link}}\">{{.Link}}</a></span>\n</body>\n</html>') ON CONFLICT DO NOTHING"); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to execute query")
	}
	return nil
}

func (r *PostgresRepository) GetSQLDB() *sqlx.DB {
	return r.db
}

func (r *PostgresRepository) WithTransaction(ctx context.Context) (withTx interface{}, commit func(), rollback func(), err error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, nil, appError.NewError().WithError(err).WithMessage("failed to begin transaction")
	}
	rollback = func() {
		if err = tx.Rollback(); err != nil {
			log.Error().Err(err).Msg("Failed to rollback transaction")
		}
	}

	commit = func() {
		if err = tx.Commit(); err != nil {
			log.Error().Err(err).Msg("Failed to commit transaction")
		}
	}

	withTx = r.WithTx(tx)

	return withTx, commit, rollback, nil
}
