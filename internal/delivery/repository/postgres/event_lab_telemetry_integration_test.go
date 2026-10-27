package postgres_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// telemetryObservation inserts one observation of the team's lab group.
func telemetryObservation(
	t *testing.T, db *testhelpers.TestDB, event, team uuid.UUID, seq int, observed time.Time, payload string,
) {
	t.Helper()
	rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, 'grp', 'agent', $4, $5, $6, 3, false, $7)`,
		uuid.Must(uuid.NewV7()), event, team, seq, observed, observed.Add(400*time.Millisecond), payload)
}

func telemetryPayload(ready bool, phase string, cpu, memory int, client string, handshake time.Time, rx, tx int64) string {
	return fmt.Sprintf(`{"labs":[{"name":"web","status":{"phase":"Ready","ready":%t,"vpnReady":%t,"devices":[`+
		`{"name":"app","ready":%t,"podPhase":%q,"cpuMillicores":"%d","memoryBytes":"%d","usageAvailable":true}]}}],`+
		`"clients":[{"name":%q,"status":{"statistics":{"rxBytes":"%d","txBytes":"%d","lastHandshakeUnix":"%d"}}}]}`,
		ready, ready, ready, phase, cpu, memory, client, rx, tx, handshake.Unix())
}

// The minute rollup keeps the memory average, readiness and last pod phase per
// device, readiness per lab, and per VPN client the traffic of every minute
// (across batches and counter resets) and the connected samples.
func TestEventLabTelemetry_ExtendedMinuteRollup(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "labminutes")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	m0 := anStart.Add(time.Hour).UTC() // a minute boundary

	// minute 0: not ready, then ready; minute 1: ready. Peer counters grow, reset at 70 s.
	h := m0.Add(-time.Minute)
	telemetryObservation(t, db, f.event, f.team, 1, m0.Add(5*time.Second), telemetryPayload(false, "Pending", 10, 1000, "p-a", h, 100, 10))
	telemetryObservation(t, db, f.event, f.team, 2, m0.Add(30*time.Second), telemetryPayload(true, "Running", 20, 2000, "p-a", h, 400, 40))
	telemetryObservation(t, db, f.event, f.team, 3, m0.Add(65*time.Second), telemetryPayload(true, "Running", 30, 3000, "p-a", h, 900, 90))
	telemetryObservation(t, db, f.event, f.team, 4, m0.Add(70*time.Second), telemetryPayload(true, "Running", 40, 5000, "p-a", h, 50, 5))                            // reset
	telemetryObservation(t, db, f.event, f.team, 5, m0.Add(100*time.Second), telemetryPayload(true, "Succeeded", 50, 7000, "p-a", m0.Add(-10*time.Minute), 150, 15)) // stale handshake

	var cursor eventAnalyticsRepo.VPNCursor
	for {
		next, read, err := repo.RollupLabUsage(ctx, f.event, cursor, 2, false) // batches of two split minute 1
		require.NoError(t, err)
		cursor = next
		if read < 2 {
			break
		}
	}

	type device struct {
		bucket                     time.Time
		samples, memSamples, ready int
		memSum                     int64
		phase                      string
	}
	rows, err := db.Pool.Query(ctx, `
SELECT bucket_at, samples, memory_samples, ready_samples, memory_sum, last_pod_phase
FROM event_lab_usage_minutes WHERE event_id = $1 ORDER BY bucket_at`, f.event)
	require.NoError(t, err)
	var devices []device
	for rows.Next() {
		var d device
		require.NoError(t, rows.Scan(&d.bucket, &d.samples, &d.memSamples, &d.ready, &d.memSum, &d.phase))
		d.bucket = d.bucket.UTC()
		devices = append(devices, d)
	}
	rows.Close()
	require.Equal(t, []device{
		{m0, 2, 2, 1, 3000, "Running"},
		{m0.Add(time.Minute), 3, 3, 3, 15000, "Succeeded"},
	}, devices)

	var labSamples, labReady, labVPN int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT sum(samples), sum(ready_samples), sum(vpn_ready_samples) FROM event_lab_minutes WHERE event_id = $1`, f.event).
		Scan(&labSamples, &labReady, &labVPN))
	require.Equal(t, [3]int{5, 4, 4}, [3]int{labSamples, labReady, labVPN})

	type client struct {
		bucket             time.Time
		samples, connected int
		rx, tx, lastRx     int64
	}
	crow, err := db.Pool.Query(ctx, `
SELECT bucket_at, samples, connected_samples, rx_bytes, tx_bytes, last_rx FROM event_vpn_client_minutes
WHERE event_id = $1 AND client_name = 'p-a' ORDER BY bucket_at`, f.event)
	require.NoError(t, err)
	var clients []client
	for crow.Next() {
		var c client
		require.NoError(t, crow.Scan(&c.bucket, &c.samples, &c.connected, &c.rx, &c.tx, &c.lastRx))
		c.bucket = c.bucket.UTC()
		clients = append(clients, c)
	}
	crow.Close()
	// minute 0: 0 (first sample, no baseline) + 300; minute 1: +500, a reset counts the new 50, then +100;
	// the last sample's handshake is stale, so it is not connected.
	require.Equal(t, []client{
		{m0, 2, 2, 300, 30, 400},
		{m0.Add(time.Minute), 3, 2, 650, 65, 150},
	}, clients)
}

// The reads give one team's rows in the range, the moderators team nothing,
// and for lab_ready the platform's own time of marking the lab ready.
func TestEventLabTelemetry_TeamReads(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "labreads")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	m0 := anStart.Add(time.Hour).UTC()
	period := eventAnalyticsModel.Period{From: m0, To: m0.Add(10 * time.Minute)}

	telemetryObservation(t, db, f.event, f.team, 1, m0.Add(5*time.Second), telemetryPayload(true, "Running", 10, 1000, "p-a", m0, 100, 10))
	telemetryObservation(t, db, f.event, f.moderators, 2, m0.Add(6*time.Second), telemetryPayload(true, "Running", 10, 1000, "p-m", m0, 100, 10))
	telemetryObservation(t, db, f.event, f.team, 3, m0.Add(20*time.Minute), telemetryPayload(true, "Running", 10, 1000, "p-a", m0, 100, 10)) // outside
	newID := func() uuid.UUID { return uuid.Must(uuid.NewV7()) }
	_, _, err := repo.RollupLabUsage(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 100, false)
	require.NoError(t, err)
	journal, err := repo.LabJournal(ctx, f.event, newID)
	require.NoError(t, err)
	_, _, err = repo.RollupLabJournal(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 100, journal, false)
	require.NoError(t, err)

	devices, err := repo.TeamDeviceMinutes(ctx, f.event, f.team, period, 100)
	require.NoError(t, err)
	require.Len(t, devices, 1)
	require.Equal(t, "app", devices[0].DeviceName)
	require.EqualValues(t, 1000, devices[0].MemorySum)
	labs, err := repo.TeamLabMinutes(ctx, f.event, f.team, period, 100)
	require.NoError(t, err)
	require.Len(t, labs, 1)
	clients, err := repo.TeamClientMinutes(ctx, f.event, f.team, period, 100)
	require.NoError(t, err)
	require.Len(t, clients, 1)
	require.Equal(t, "p-a", clients[0].ClientName)
	none, err := repo.TeamDeviceMinutes(ctx, f.event, f.moderators, period, 100)
	require.NoError(t, err)
	require.Empty(t, none, "the moderators team is never listed")

	// The platform marked the lab ready 4 s after the agent saw it.
	rtExec(t, db, `INSERT INTO lab_bindings (id, event_id, event_team_id, event_challenge_id, lab_group_name, lab_name, readiness, created_at)
VALUES ($1, $2, $3, $4, 'grp', 'web', 1, $5)`, uuid.Must(uuid.NewV7()), f.event, f.team, f.challenge, m0)
	rtExec(t, db, `INSERT INTO event_stand_transitions (event_id, team_id, source, challenge_id, generation, from_status, to_status, at)
VALUES ($1, $2, 'lab', $3, 1, 0, 1, $4)`, f.event, f.team, f.challenge, m0.Add(9*time.Second))
	transitions, err := repo.TeamLabTransitions(ctx, f.event, f.team, period, nil, 100)
	require.NoError(t, err)
	var ready *eventAnalyticsRepo.LabTransition
	for i := range transitions {
		if transitions[i].Kind == "lab_ready" {
			ready = &transitions[i]
		}
	}
	require.NotNil(t, ready)
	require.Equal(t, m0.Add(5*time.Second), ready.At.UTC())
	require.NotNil(t, ready.PlatformReadyAt)
	require.Equal(t, 4*time.Second, ready.PlatformReadyAt.Sub(ready.At))
	// Two platform changes at the very same instant must not double the transition.
	rtExec(t, db, `INSERT INTO event_stand_transitions (event_id, team_id, source, challenge_id, generation, from_status, to_status, at)
VALUES ($1, $2, 'lab', $3, 2, 0, 1, $4)`, f.event, f.team, f.challenge, m0.Add(9*time.Second))
	transitions, err = repo.TeamLabTransitions(ctx, f.event, f.team, period, []string{"lab_ready"}, 100)
	require.NoError(t, err)
	require.Len(t, transitions, 1)
	only, err := repo.TeamLabTransitions(ctx, f.event, f.team, period, []string{"client_connected"}, 100)
	require.NoError(t, err)
	require.Len(t, only, 1)
	require.Nil(t, only[0].PlatformReadyAt)
}

// One bad value in a payload (negative, fractional, text, beyond bigint) must not stop the rollups: the batch
// passes, the cursor moves on and the good observations are counted.
func TestEventLabTelemetry_PoisonedPayloadDoesNotStopTheRollups(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "labpoison")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	m0 := anStart.Add(time.Hour).UTC()
	poison := func(rx, tx, memory, cpu, restarts, handshake string) string {
		return fmt.Sprintf(`{"labs":[{"name":"web","status":{"ready":true,"devices":[{"name":"app","ready":true,"podPhase":"Running",`+
			`"cpuMillicores":%s,"memoryBytes":%s,"restartCount":%s,"usageAvailable":true}]}}],`+
			`"clients":[{"name":"p-a","status":{"statistics":{"rxBytes":%s,"txBytes":%s,"lastHandshakeUnix":%s}}}]}`,
			cpu, memory, restarts, rx, tx, handshake)
	}
	telemetryObservation(t, db, f.event, f.team, 1, m0.Add(1*time.Second), poison(`"-5"`, `"abc"`, `"99999999999999999999"`, `"1.5"`, `"-1"`, `"1e10"`))
	telemetryObservation(t, db, f.event, f.team, 2, m0.Add(2*time.Second), poison(`-7`, `1.5`, `-1`, `"x"`, `null`, `99999999999999999999`))
	telemetryObservation(t, db, f.event, f.team, 3, m0.Add(3*time.Second), poison(`"100"`, `"10"`, `"2000"`, `"30"`, `"0"`, strconv.FormatInt(m0.Unix(), 10)))
	telemetryObservation(t, db, f.event, f.team, 4, m0.Add(4*time.Second), `{"labs":"nope","clients":[1,"x",{"name":7}]}`)

	cursor, read, err := repo.RollupLabUsage(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 100, false)
	require.NoError(t, err)
	require.Equal(t, 4, read)
	require.NotEqual(t, uuid.Nil, cursor.ID, "the cursor moved past the bad observations")

	var samples, memorySum, cpuSum int64
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT sum(samples), sum(memory_sum), sum(cpu_sum) FROM event_lab_usage_minutes WHERE event_id = $1`, f.event).
		Scan(&samples, &memorySum, &cpuSum))
	require.EqualValues(t, 3, samples)
	require.EqualValues(t, 2000, memorySum, "only the good value counts")
	require.EqualValues(t, 30, cpuSum)

	vpn, _, vpnRead, err := repo.VPNSamples(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 100)
	require.NoError(t, err)
	require.Equal(t, 4, vpnRead)
	require.Len(t, vpn, 1, "only the good handshake makes a sample")

	journal, err := repo.LabJournal(ctx, f.event, func() uuid.UUID { return uuid.Must(uuid.NewV7()) })
	require.NoError(t, err)
	_, journalRead, err := repo.RollupLabJournal(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 100, journal, false)
	require.NoError(t, err)
	require.Equal(t, 4, journalRead)
}
