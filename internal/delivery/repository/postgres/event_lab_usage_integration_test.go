package postgres_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// rawStandResources is the stand resource statement as it read the raw
// payloads before the lab usage rollup; the rollup must give the same answer.
const rawStandResources = `
WITH obs AS (
    SELECT o.event_team_id, o.payload
    FROM event_lab_observations o
    JOIN event_teams t ON t.id = o.event_team_id AND NOT t.moderators
    WHERE o.event_id = $1 AND o.observed_at >= $2::timestamptz AND o.observed_at < $3::timestamptz
), devices AS (
    SELECT obs.event_team_id,
           COALESCE(lab ->> 'name', '') AS lab_name,
           COALESCE(dev ->> 'name', '') AS device_name,
           CASE WHEN dev ->> 'usageAvailable' = 'true' THEN NULLIF(dev ->> 'cpuMillicores', '')::bigint END AS cpu,
           CASE WHEN dev ->> 'usageAvailable' = 'true' THEN NULLIF(dev ->> 'memoryBytes', '')::bigint END AS memory,
           COALESCE(NULLIF(dev ->> 'restartCount', '')::bigint, 0) AS restarts
    FROM obs
    CROSS JOIN LATERAL jsonb_array_elements(
            CASE WHEN jsonb_typeof(obs.payload -> 'labs') = 'array' THEN obs.payload -> 'labs' ELSE '[]'::jsonb END) lab
    CROSS JOIN LATERAL jsonb_array_elements(
            CASE WHEN jsonb_typeof(lab -> 'status' -> 'devices') = 'array' THEN lab -> 'status' -> 'devices' ELSE '[]'::jsonb END) dev
), per_device AS (
    SELECT event_team_id, lab_name, device_name,
           max(cpu) AS peak_cpu, max(memory) AS peak_memory, max(restarts) AS restarts
    FROM devices
    GROUP BY event_team_id, lab_name, device_name
)
SELECT event_team_id, count(*)::bigint, COALESCE(sum(peak_cpu), 0)::bigint, COALESCE(sum(peak_memory), 0)::bigint,
       COALESCE(sum(restarts), 0)::bigint, count(*) FILTER (WHERE restarts > 0)::bigint
FROM per_device
GROUP BY event_team_id`

func rawResources(
	t *testing.T,
	db *testhelpers.TestDB,
	event uuid.UUID,
	period eventAnalyticsModel.Period,
) []eventAnalyticsRepo.StandResources {
	t.Helper()
	rows, err := db.Pool.Query(
		context.Background(),
		rawStandResources,
		event,
		period.From,
		period.To,
	)
	require.NoError(t, err)
	defer rows.Close()
	var out []eventAnalyticsRepo.StandResources
	for rows.Next() {
		var r eventAnalyticsRepo.StandResources
		require.NoError(
			t,
			rows.Scan(
				&r.TeamID,
				&r.Devices,
				&r.PeakCPUMillis,
				&r.PeakMemoryBytes,
				&r.Restarts,
				&r.RestartedDevices,
			),
		)
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

// rollupAll folds every observation of the event in batches of two, so the
// minutes spanning batches are merged, and returns the last cursor.
func rollupAll(
	t *testing.T,
	repo *eventAnalyticsRepo.Repository,
	event uuid.UUID,
) eventAnalyticsRepo.VPNCursor {
	t.Helper()
	var cursor eventAnalyticsRepo.VPNCursor
	for range 100 {
		next, read, err := repo.RollupLabUsage(context.Background(), event, cursor, 2)
		require.NoError(t, err)
		cursor = next
		if read < 2 {
			return cursor
		}
	}
	t.Fatal("the rollup never caught up")
	return cursor
}

// The lab usage rollup answers the stand resources exactly like the raw
// payloads did, in whatever batches it ran, and a re-run changes nothing.
func TestEventLabUsage_RollupMatchesRawPayloads(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "labusage")
	period := eventAnalyticsModel.Period{From: anStart, To: anStart.Add(4 * time.Hour)}

	seq := 0
	observe := func(team uuid.UUID, group string, at time.Time, payload string) {
		seq++
		rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, $4, 'agent', $5, $6, $6, 1, false, $7)`, uuid.Must(uuid.NewV7()), f.event, team, group, seq, at, payload)
	}
	device := func(name string, available bool, cpu, memory, restarts int) string {
		return fmt.Sprintf(
			`{"name":%q,"usageAvailable":%t,"cpuMillicores":"%d","memoryBytes":%d,"restartCount":%d}`,
			name,
			available,
			cpu,
			memory,
			restarts,
		)
	}
	lab := func(name string, devices ...string) string {
		return fmt.Sprintf(
			`{"name":%q,"status":{"devices":[%s]}}`,
			name,
			strings.Join(devices, ","),
		)
	}
	payload := func(labs ...string) string {
		return `{"labs":[` + strings.Join(labs, ",") + `]}`
	}

	// Several samples inside the same minutes, two lab groups, a device that
	// never reports usage, an observation without devices, and an unrelated
	// payload; the moderators team is in the data but not in the answer.
	for i := range 12 {
		at := anStart.Add(time.Duration(i)*20*time.Second + 3*time.Second)
		observe(f.team, "group-a", at, payload(
			lab(
				"web",
				device("app", true, 100+i*30, 1000+(i%4)*500, i/5),
				device("db", false, 0, 0, i/6),
			),
			lab("net", device("proxy", true, 50, 4000-i*100, 0)),
		))
		observe(
			f.team,
			"group-b",
			at.Add(time.Second),
			payload(lab("web", device("app", true, 90, 700, 1))),
		)
		observe(f.moderators, "group-m", at, payload(lab("web", device("app", true, 999, 9999, 9))))
	}
	observe(f.team, "group-a", anStart.Add(5*time.Minute), `{"labs":[]}`)
	observe(f.team, "group-a", anStart.Add(6*time.Minute), `{"clients":[]}`)

	cursor := rollupAll(t, repo, f.event)
	require.NotEqual(t, uuid.Nil, cursor.ID)

	got, err := repo.StandResources(ctx, f.event, period)
	require.NoError(t, err)
	want := rawResources(t, db, f.event, period)
	require.NotEmpty(t, want)
	require.Equal(t, want, got)
	require.EqualValues(t, 3, got[0].Devices, "app, db and proxy")

	// A sub-period matches too (minute-aligned bounds).
	sub := eventAnalyticsModel.Period{
		From: anStart.Add(2 * time.Minute),
		To:   anStart.Add(3 * time.Minute),
	}
	got, err = repo.StandResources(ctx, f.event, sub)
	require.NoError(t, err)
	require.Equal(t, rawResources(t, db, f.event, sub), got)

	// Idempotent: nothing left to read, nothing changes.
	again, read, err := repo.RollupLabUsage(ctx, f.event, cursor, 2)
	require.NoError(t, err)
	require.Zero(t, read)
	require.Equal(t, cursor, again)
	var samples, cpuSamples, cpuSum int64
	require.NoError(t, db.Pool.QueryRow(ctx, `
SELECT samples, cpu_samples, cpu_sum FROM event_lab_usage_minutes
WHERE event_id = $1 AND team_id = $2 AND lab_group_name = 'group-a' AND device_name = 'app' AND bucket_at = $3`,
		f.event, f.team, anStart).Scan(&samples, &cpuSamples, &cpuSum))
	require.EqualValues(t, 3, samples, "three samples of the first minute, merged across batches")
	require.EqualValues(t, 3, cpuSamples)
	require.EqualValues(t, 100+130+160, cpuSum)

	// The stored cursor is the one the rollup returned.
	var storedAt time.Time
	var storedID uuid.UUID
	require.NoError(
		t,
		db.Pool.QueryRow(ctx, `SELECT lab_usage_cursor_received_at, lab_usage_cursor_id FROM event_analytics_rollups WHERE event_id = $1`, f.event).
			Scan(&storedAt, &storedID),
	)
	require.Equal(t, cursor.ID, storedID)
	require.True(t, cursor.ReceivedAt.Equal(storedAt))
}

// Raw snapshots go a day after arrival once the rollups passed them; the
// latest snapshot of each lab group stays, and unrolled ones are never removed.
func TestEventLabUsage_PurgeRolledUpObservations(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	retention := retentionRepo.New(db.Queries)
	f := anSeed(t, db, "labpurge")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)

	seq := 0
	ids := map[string]uuid.UUID{}
	observe := func(name, group string, at time.Time) {
		seq++
		id := uuid.Must(uuid.NewV7())
		ids[name] = id
		rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, $4, 'agent', $5, $6, $6, 1, false, '{"labs":[]}')`, id, f.event, f.team, group, seq, at)
	}
	exists := func(name string) bool {
		var n int
		require.NoError(
			t,
			db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_lab_observations WHERE id = $1`, ids[name]).
				Scan(&n),
		)
		return n == 1
	}
	now := anStart.Add(72 * time.Hour)
	cutoff := now.Add(-24 * time.Hour)

	observe("a-old-1", "a", anStart)
	observe("a-old-2", "a", anStart.Add(time.Minute))
	observe("a-old-3", "a", anStart.Add(2*time.Minute))
	observe("b-old-only", "b", anStart.Add(3*time.Minute))
	observe("a-recent", "a", now.Add(-time.Hour))
	observe("a-unrolled", "a", now.Add(-30*time.Minute))

	// Nothing is rolled up yet: nothing goes.
	n, err := retention.PurgeRolledUpEventLabObservations(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Zero(t, n)

	// Only the lab usage rollup passed them: the VPN rollup still has to.
	var at time.Time
	require.NoError(
		t,
		db.Pool.QueryRow(ctx, `SELECT received_at FROM event_lab_observations WHERE id = $1`, ids["a-recent"]).
			Scan(&at),
	)
	id := ids["a-recent"]
	rtExec(
		t,
		db,
		`INSERT INTO event_analytics_rollups (event_id, lab_usage_cursor_received_at, lab_usage_cursor_id) VALUES ($1, $2, $3)`,
		f.event,
		at,
		id,
	)
	n, err = retention.PurgeRolledUpEventLabObservations(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Zero(t, n)

	// Both passed; a-unrolled is after both cursors.
	rtExec(
		t,
		db,
		`UPDATE event_analytics_rollups SET vpn_cursor_received_at = $2, vpn_cursor_id = $3 WHERE event_id = $1`,
		f.event,
		at,
		id,
	)
	// Both rollups passed, but the archive has not: raw rows are never deleted before they are archived.
	n, err = retention.PurgeRolledUpEventLabObservations(ctx, cutoff, 100)
	require.NoError(t, err)
	require.Zero(t, n)

	archive := eventAnalyticsRepo.New(db.Queries)
	var archived int
	for cursor := (eventAnalyticsRepo.VPNCursor{}); ; {
		next, rows, aerr := archive.ArchiveLabObservations(ctx, f.event, cursor, now, uuid.Must(uuid.NewV7()))
		require.NoError(t, aerr)
		if rows == 0 {
			break
		}
		archived += rows
		cursor = next
	}
	require.Equal(t, 6, archived)
	n, err = retention.PurgeRolledUpEventLabObservations(ctx, cutoff, 100)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	require.False(t, exists("a-old-1"))
	require.False(t, exists("a-old-2"))
	require.False(t, exists("a-old-3"), "a newer snapshot of group a exists")
	require.True(t, exists("b-old-only"), "the latest snapshot of a group stays")
	require.True(t, exists("a-recent"), "newer than the cutoff")
	require.True(t, exists("a-unrolled"), "not rolled up yet")

	// The latest-state reader still sees every group.
	latest, err := db.Queries.ListLatestEventLabObservations(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, latest, 2)
}
