package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestSMTPRateLimits_SavedPerTransportAndConstrained(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	eventID := mailEvent(t, db, "limits", now.Add(24*time.Hour), nil)

	platform, err := db.Queries.InsertPlatformSMTPProvider(ctx, providerParams("SES", "email-smtp.eu-north-1.amazonaws.com", now,
		pgtype.Float8{Float64: 14, Valid: true}, pgtype.Int4{Int32: 50000, Valid: true}))
	require.NoError(t, err)
	require.Equal(t, 14.0, platform.MaxPerSecond.Float64)
	require.Equal(t, int32(50000), platform.DailyQuota.Int32)

	// The Event transport has limits of its own; NULL = not set.
	event, err := db.Queries.UpsertEventSMTPConfig(ctx, postgres.UpsertEventSMTPConfigParams{
		ID: uuid.Must(uuid.NewV7()), ScopeEventID: uuid.NullUUID{UUID: eventID, Valid: true},
		Host: "smtp.uni.edu", Port: 587, TlsMode: "starttls", UpdatedAt: now,
		MaxPerSecond: pgtype.Float8{Float64: 0.5, Valid: true},
	})
	require.NoError(t, err)
	require.Equal(t, 0.5, event.MaxPerSecond.Float64)
	require.False(t, event.DailyQuota.Valid)

	// Saving again with NULL clears a limit.
	cleared := updateParams(platform, pgtype.Float8{}, pgtype.Int4{})
	platform, err = db.Queries.UpdatePlatformSMTPProvider(ctx, cleared)
	require.NoError(t, err)
	require.False(t, platform.MaxPerSecond.Valid)
	require.False(t, platform.DailyQuota.Valid)

	// Zero and negative limits are rejected by the database too.
	_, err = db.Queries.UpdatePlatformSMTPProvider(ctx, updateParams(platform, pgtype.Float8{Float64: 0, Valid: true}, pgtype.Int4{}))
	require.Error(t, err)
	_, err = db.Queries.UpdatePlatformSMTPProvider(ctx, updateParams(platform, pgtype.Float8{}, pgtype.Int4{Int32: -1, Valid: true}))
	require.Error(t, err)
}

func TestCountEmailDeliveredSince_RollingWindowPerTransport(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	user := mailUser(t, db, "quota@example.org")
	eventA := mailEvent(t, db, "quotaa", now.Add(24*time.Hour), nil)
	eventB := mailEvent(t, db, "quotab", now.Add(24*time.Hour), nil)

	target := func(scope *uuid.UUID, channel, status, transport string, at time.Time) {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		arg := postgres.CreateDispatchParams{ID: id, NotificationType: "smtp_test", RecipientUserID: user, Status: "done"}
		if scope != nil {
			arg.ScopeEventID = uuid.NullUUID{UUID: *scope, Valid: true}
		}
		_, err := db.Queries.CreateDispatch(ctx, arg)
		require.NoError(t, err)
		require.NoError(t, db.Queries.UpsertDispatchTarget(ctx, postgres.UpsertDispatchTargetParams{
			DispatchID: id, Channel: channel, Status: status, Transport: transport,
		}))
		_, err = db.Pool.Exec(ctx, `UPDATE notification_dispatch_targets SET updated_at = $2 WHERE dispatch_id = $1`, id, at)
		require.NoError(t, err)
	}
	recent, old := now.Add(-time.Hour), now.Add(-25*time.Hour)
	target(nil, "email", "done", "platform", recent)
	target(nil, "email", "done", "platform", recent)
	target(&eventA, "email", "done", "platform", recent) // Event mail that fell back to the platform
	target(nil, "email", "done", "platform", old)        // outside the window
	target(nil, "email", "error", "platform", recent)    // not delivered
	target(nil, "email", "deferred", "platform", recent) // not delivered yet
	target(nil, "in_app", "done", "", recent)            // other channel
	target(nil, "email", "done", "env", recent)
	target(&eventA, "email", "done", "event", recent)
	target(&eventA, "email", "done", "event", recent)
	target(&eventB, "email", "done", "event", recent)

	count := func(transport string, event *uuid.UUID) int64 {
		t.Helper()
		arg := postgres.CountEmailDeliveredSinceParams{Transport: transport, Since: now.Add(-24 * time.Hour)}
		if event != nil {
			arg.EventID = uuid.NullUUID{UUID: *event, Valid: true}
		}
		n, err := db.Queries.CountEmailDeliveredSince(ctx, arg)
		require.NoError(t, err)
		return n
	}
	require.EqualValues(t, 3, count("platform", nil), "everything through the platform, fallback included")
	require.EqualValues(t, 1, count("env", nil))
	require.EqualValues(t, 2, count("event", &eventA), "an Event counts only its own transport")
	require.EqualValues(t, 1, count("event", &eventB))
}

// TestSMTPRateLimitsMigration: down drops the columns and the index, up
// brings them back.
func TestSMTPRateLimitsMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	up := readMigration(t, "0131_smtp_rate_limits.up.sql")
	down := readMigration(t, "0131_smtp_rate_limits.down.sql")

	columns := func() int {
		var n int
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_name = 'mail_smtp_configs' AND column_name IN ('max_per_second', 'daily_quota')`).Scan(&n))
		return n
	}
	index := func() bool {
		var ok bool
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_indexes
			WHERE indexname = 'notification_dispatch_targets_email_done_idx')`).Scan(&ok))
		return ok
	}
	require.Equal(t, 2, columns())
	require.True(t, index())

	_, err := db.Pool.Exec(ctx, down)
	require.NoError(t, err)
	require.Equal(t, 0, columns())
	require.False(t, index())

	_, err = db.Pool.Exec(ctx, up)
	require.NoError(t, err)
	require.Equal(t, 2, columns())
	require.True(t, index())
}
