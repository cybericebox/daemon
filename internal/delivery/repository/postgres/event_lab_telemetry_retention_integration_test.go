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
	rtExec(t, db, `INSERT INTO event_lab_transitions (id, event_id, team_id, lab_group_name, client_name, kind, at, received_at)
VALUES ($1, $2, $3, 'g', 'p-a', 'client_connected', $4, $4)`, uuid.Must(uuid.NewV7()), event, team, at)
	rtExec(t, db, `INSERT INTO event_lab_journal_states (event_id, team_id, lab_group_name, kind, lab_name, name, state)
VALUES ($1, $2, 'g', 'lab', 'l', 'l', '{}') ON CONFLICT DO NOTHING`, event, team)
	rtExec(t, db, `INSERT INTO event_lab_journal_states (event_id, team_id, lab_group_name, kind, lab_name, name, state)
VALUES ($1, $2, 'g', 'client', '', 'p-a', '{}') ON CONFLICT DO NOTHING`, event, team)
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

// The derived lab telemetry has two periods counted from the event's finish: the data about containers, labs and
// teams goes after the longer one, the data about people (client minutes, client transitions and states, the raw
// archive) after the shorter one. An event with no end loses only rows older than the cutoffs.
func TestRetention_LabTelemetryTwoPeriodsFromTheFinish(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := retentionRepo.New(db.Queries)
	general := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC) // now minus the telemetry period
	participants := general.AddDate(0, 0, 200)              // now minus the (shorter) participants period
	between := general.AddDate(0, 0, 100)                   // finished after the general cutoff, before the participants one
	old, recent := general.Add(-48*time.Hour), participants.Add(48*time.Hour)

	newEvent := func(tag string) (uuid.UUID, uuid.UUID) {
		e := mustSeedEventForParticipants(t, db, tag)
		user := mustSeedUser(t, db, tag+"@test.test")
		return e.ID, eaTeam(t, db, e.ID, user, "Blue")
	}
	finish := func(event uuid.UUID, at time.Time) {
		rtExec(t, db, `UPDATE events SET finish_at = $2, withdraw_at = $3, start_at = $4, publish_at = $4 WHERE id = $1`,
			event, at, at.AddDate(5, 0, 0), at.Add(-72*time.Hour))
	}
	// Finished long ago: everything is past both periods.
	closed, closedTeam := newEvent("rtlabclosed")
	finish(closed, old)
	// Finished between the cutoffs: the people are gone, the teams' data stays (the withdrawal does not matter).
	middle, middleTeam := newEvent("rtlabmiddle")
	finish(middle, between)
	// No end at all: rolling by the row's own time.
	permanent, permanentTeam := newEvent("rtlabperm")
	rtExec(t, db, `UPDATE events SET finish_at = NULL, withdraw_at = NULL, manual_finished_at = NULL WHERE id = $1`, permanent)

	seedLabTelemetry(t, db, closed, closedTeam, recent)
	seedLabTelemetry(t, db, middle, middleTeam, recent)
	seedLabTelemetry(t, db, permanent, permanentTeam, old)
	seedLabTelemetry(t, db, permanent, permanentTeam, recent)

	n := rtDrain(t, 2, func() (int64, error) { return repo.PurgeEventLabTelemetry(t.Context(), general, participants, 2) })
	// closed: all 8 rows; middle: the 4 rows about people (client minutes, transition, state, archive);
	// permanent: the 6 old time-bound rows (usage, lab, client minutes, two transitions, the archive).
	require.EqualValues(t, 8+4+6, n)

	for table, rows := range labTelemetryRows(t, db, closed) {
		require.Zero(t, rows, "closed event %s", table)
	}
	got := labTelemetryRows(t, db, middle)
	for _, table := range []string{"event_lab_usage_minutes", "event_lab_minutes", "event_lab_transitions", "event_lab_journal_states"} {
		require.Equal(t, 1, got[table], "the teams' data of %s stays", table)
	}
	for _, table := range []string{"event_vpn_client_minutes", "event_lab_observation_archives"} {
		require.Zero(t, got[table], "the people's data of %s goes with the shorter period", table)
	}
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE event_id = $1 AND client_name IS NOT NULL`, middle))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_journal_states WHERE event_id = $1 AND kind = 'client'`, middle))
	want := map[string]int{
		"event_lab_usage_minutes": 1, "event_lab_minutes": 1, "event_vpn_client_minutes": 1, "event_lab_transitions": 2,
		"event_lab_journal_states": 2, "event_lab_observation_archives": 1,
	}
	require.Equal(t, want, labTelemetryRows(t, db, permanent), "permanent event keeps its recent rows and its states")
}
