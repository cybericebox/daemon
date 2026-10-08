package postgres_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRetentionSchemaAndKnownQuotaSurviveScopeDelete(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for _, table := range []string{"event_lab_retention_pins", "event_lab_group_retention_pins", "event_lab_generations", "event_stage_lab_runtime_memberships", "event_lab_reveal_barriers"} {
		var count int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`, table).Scan(&count))
		require.Equal(t, 1, count)
	}
	// Scope IDs deliberately carry no cascading foreign keys in durable ledgers.
	var cascades int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conrelid IN ('event_team_labs'::regclass,'event_team_group_allocations'::regclass) AND contype='f' AND confdeltype='c'`).Scan(&cascades))
	require.Zero(t, cascades)
}
