package postgres_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
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
		next, read, err := repo.RollupLabUsage(ctx, f.event, cursor, 2) // batches of two split minute 1
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
