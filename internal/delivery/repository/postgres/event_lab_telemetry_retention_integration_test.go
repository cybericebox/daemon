package postgres_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// labTelemetryTables are the derived lab telemetry tables and how many rows a
// seeded event gets in each.
var labTelemetryTables = []string{
	"event_lab_usage_minutes", "event_lab_minutes", "event_vpn_client_minutes", "event_lab_transitions",
	"event_lab_journal_states", "event_lab_observation_archives",
}

// seedLabTelemetry gives the event one row in every derived table, at the time at.
func seedLabTelemetry(t *testing.T, db *testhelpers.TestDB, event, team uuid.UUID, at time.Time) {
	t.Helper()
	rtExec(t, db, `INSERT INTO event_lab_usage_minutes (event_id, team_id, lab_group_name, lab_name, device_name, bucket_at, samples, cpu_samples, cpu_sum, restart_max)
VALUES ($1, $2, 'g', 'l', 'd', $3, 1, 0, 0, 0)`, event, team, at)
	rtExec(t, db, `INSERT INTO event_lab_minutes (event_id, team_id, lab_group_name, lab_name, bucket_at, samples, ready_samples, vpn_ready_samples)
VALUES ($1, $2, 'g', 'l', $3, 1, 1, 1)`, event, team, at)
	rtExec(t, db, `INSERT INTO event_vpn_client_minutes (event_id, team_id, lab_group_name, client_name, bucket_at, samples, connected_samples, rx_bytes, tx_bytes, last_rx, last_tx, last_observed_at)
VALUES ($1, $2, 'g', 'p-a', $3, 1, 1, 0, 0, 0, 0, $3)`, event, team, at)
	rtExec(t, db, `INSERT INTO event_lab_transitions (id, event_id, team_id, lab_group_name, kind, at, received_at)
VALUES ($1, $2, $3, 'g', 'lab_ready', $4, $4)`, uuid.Must(uuid.NewV7()), event, team, at)
	rtExec(t, db, `INSERT INTO event_lab_journal_states (event_id, team_id, lab_group_name, kind, lab_name, name, state)
VALUES ($1, $2, 'g', 'lab', 'l', 'l', '{}') ON CONFLICT DO NOTHING`, event, team)
	rtExec(t, db, `INSERT INTO event_lab_observation_archives (id, event_id, period_start, period_end, row_count, first_received_at, first_id, last_received_at, last_id, codec, data)
VALUES ($1, $2, $3, $4, 1, $3, $1, $3, $1, 'zstd-ndjson-v1', '\x00')`, uuid.Must(uuid.NewV7()), event, at.Truncate(time.Hour), at.Truncate(time.Hour).Add(time.Hour))
}

func labTelemetryRows(t *testing.T, db *testhelpers.TestDB, event uuid.UUID) map[string]int {
	t.Helper()
	out := map[string]int{}
	for _, table := range labTelemetryTables {
		out[table] = rtCount(t, db, fmt.Sprintf(`SELECT count(*) FROM %s WHERE event_id = $1`, table), event)
	}
	return out
}

// The derived lab telemetry goes the telemetry period after the event's access
// closes (the withdrawal, else the finish); an event with no end loses only its
// rows older than the period.
func TestRetention_LabTelemetryFollowsAccessClose(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := retentionRepo.New(db.Queries)
	cut := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) // now minus the telemetry period
	old, recent := cut.Add(-48*time.Hour), cut.Add(48*time.Hour)

	newEvent := func(tag string) (uuid.UUID, uuid.UUID) {
		e := mustSeedEventForParticipants(t, db, tag)
		user := mustSeedUser(t, db, tag+"@test.test")
		return e.ID, eaTeam(t, db, e.ID, user, "Blue")
	}
	// Access closed long before the cutoff: finish and withdrawal both past.
	closed, closedTeam := newEvent("rtlabclosed")
	rtExec(t, db, `UPDATE events SET finish_at = $2, withdraw_at = $3, start_at = $4, publish_at = $4 WHERE id = $1`,
		closed, old.Add(-24*time.Hour), old, old.Add(-72*time.Hour))
	// Finished before the cutoff but the withdrawal (access) is still ahead.
	withdrawing, withdrawingTeam := newEvent("rtlabwithdraw")
	rtExec(t, db, `UPDATE events SET finish_at = $2, withdraw_at = $3, start_at = $4, publish_at = $4 WHERE id = $1`,
		withdrawing, old, recent, old.Add(-72*time.Hour))
	// No end at all: rolling by the row's own time.
	permanent, permanentTeam := newEvent("rtlabperm")
	rtExec(t, db, `UPDATE events SET finish_at = NULL, withdraw_at = NULL, manual_finished_at = NULL WHERE id = $1`, permanent)

	seedLabTelemetry(t, db, closed, closedTeam, recent) // newer than the cutoff, but its event closed long ago
	seedLabTelemetry(t, db, withdrawing, withdrawingTeam, old)
	seedLabTelemetry(t, db, permanent, permanentTeam, old)
	seedLabTelemetry(t, db, permanent, permanentTeam, recent)

	n := rtDrain(t, 2, func() (int64, error) { return repo.PurgeEventLabTelemetry(t.Context(), cut, 2) })
	require.EqualValues(t, 6+5, n, "closed event: 6 tables; permanent: its old rows in 5 tables (states have no time)")

	for table, rows := range labTelemetryRows(t, db, closed) {
		require.Zero(t, rows, "closed event %s", table)
	}
	for table, rows := range labTelemetryRows(t, db, withdrawing) {
		require.Equal(t, 1, rows, "access still open %s", table)
	}
	for table, rows := range labTelemetryRows(t, db, permanent) {
		// Its recent row stays everywhere; the states have no time of their own and stay with the event.
		require.Equal(t, 1, rows, "permanent event %s", table)
	}
}
