package postgres_test

import (
	"testing"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The migrations of the hardening work (0205-0208) go down and up again; the full chain cannot go down to an empty
// schema: an old down file (0015, events) fails on a table a later migration created (mail_smtp_configs), a defect
// older than this branch.
func TestRecentMigrationsGoDownAndUp(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	sqlDB := stdlib.OpenDBFromPool(db.Pool)
	defer func() { _ = sqlDB.Close() }()
	driver, err := migratepgx.WithInstance(sqlDB, &migratepgx.Config{})
	require.NoError(t, err)
	m, err := migrate.NewWithDatabaseInstance("file://migrations", "postgres", driver)
	require.NoError(t, err)
	// The migrator holds a connection of the pool; release it or the pool cannot close at the end of the test.
	defer func() { _, _ = m.Close() }()

	before, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.GreaterOrEqual(t, before, uint(208))
	require.NoError(t, m.Steps(-int(before-204)), "down to 0204")
	down, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, uint(204), down)
	require.NoError(t, m.Up(), "and up again")
	after, dirty, err := m.Version()
	require.NoError(t, err)
	require.False(t, dirty)
	require.Equal(t, before, after)
	require.ErrorIs(t, m.Up(), migrate.ErrNoChange)
}
