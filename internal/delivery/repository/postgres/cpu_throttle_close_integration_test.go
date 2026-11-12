package postgres_test

import (
	"context"
	"os"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Migration 0206 closes the open CPU throttling requests (hundreds on the khctf day) without deleting anything,
// and leaves every other request alone.
func TestMigration0206ClosesTheOpenCPUThrottleRequests(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	admin := mustSeedUser(t, db, "throttle-admin@example.com")
	insert := func(typ, subject string, resolved bool) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		_, err := db.Pool.Exec(ctx, `INSERT INTO in_app_notifications (id, user_id, title, notification_type, subject_ref, action_required, category, created_at)
			VALUES ($1, $2, 'T', $3, $4, true, 'requests', now())`, id, admin, typ, subject)
		require.NoError(t, err)
		if resolved {
			_, err = db.Pool.Exec(ctx, `UPDATE in_app_notifications SET resolved_at=now(), resolution='resolved' WHERE id=$1`, id)
			require.NoError(t, err)
		}
		return id
	}
	open := insert("resources.cpu.throttled", "cpu_throttle:run-1/web", false)
	openToo := insert("resources.cpu.throttled", "cpu_throttle:run-2/db", false)
	closedUnread := insert("resources.cpu.throttled", "cpu_throttle:run-3/web", true)
	other := insert("event.lab.failed", "lab_failed:1", false)

	script, err := os.ReadFile("migrations/0206_close_cpu_throttle_requests.up.sql")
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, string(script))
	require.NoError(t, err)

	state := func(id uuid.UUID) (resolved, read bool, resolution string) {
		var res, rd *string
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT resolved_at::text, read_at::text, COALESCE(resolution,'') FROM in_app_notifications WHERE id=$1`, id).Scan(&res, &rd, &resolution))
		return res != nil, rd != nil, resolution
	}
	for _, id := range []uuid.UUID{open, openToo, closedUnread} {
		resolved, read, resolution := state(id)
		require.True(t, resolved && read, "closed and read")
		require.Equal(t, "resolved", resolution)
	}
	resolved, read, _ := state(other)
	require.False(t, resolved || read, "other requests are untouched")
	var total int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM in_app_notifications`).Scan(&total))
	require.Equal(t, 4, total, "nothing is deleted")
	var remembered int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_subject_resolutions WHERE subject_ref LIKE 'cpu_throttle:%'`).Scan(&remembered))
	require.Equal(t, 2, remembered)
}
