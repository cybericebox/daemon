package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Every connection the daemon opens comes from the main or the job pool: pooled statements, transactions and the
// connections the advisory/leader locks hijack for good. Each must reach the server with the catalog writer
// capability, or the guards of migration 0194/0195 reject its catalog writes with 40001.
func TestProductionPoolsDeclareCatalogWriterOnEveryConnection(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	cfg := &config.PostgresConfig{MaxConnLifetime: 30 * time.Minute, MaxConnIdleTime: 5 * time.Minute}
	for _, size := range []int{32, 8} { // main, job
		pc, err := postgres.NewPoolConfigForTest(db.Pool.Config().ConnString(), size, cfg)
		require.NoError(t, err)
		pool, err := pgxpool.NewWithConfig(ctx, pc)
		require.NoError(t, err)
		t.Cleanup(pool.Close)

		var marker string
		require.NoError(t, pool.QueryRow(ctx, `SELECT current_setting('cybericebox.catalog_metadata_writer', true)`).Scan(&marker))
		require.Equal(t, "1", marker, "pooled connection, size %d", size)

		c, err := pool.Acquire(ctx)
		require.NoError(t, err)
		hijacked := c.Hijack()
		t.Cleanup(func() { _ = hijacked.Close(context.Background()) })
		require.NoError(t, hijacked.QueryRow(ctx, `SELECT current_setting('cybericebox.catalog_metadata_writer', true)`).Scan(&marker))
		require.Equal(t, "1", marker, "hijacked connection, size %d", size)

		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `INSERT INTO exercises(id, name, created_at, updated_at) VALUES ($1, 'Capable writer', now(), now())`,
			uuid.Must(uuid.NewV7()))
		require.NoError(t, err, "a guarded catalog write must pass on a production pool, size %d", size)
		require.NoError(t, tx.Rollback(ctx))
	}
}
