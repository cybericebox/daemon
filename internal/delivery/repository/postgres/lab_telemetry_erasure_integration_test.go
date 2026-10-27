package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/retention"
)

type fixedPolicy struct{}

func (fixedPolicy) RetentionPolicy(context.Context) (retentionModel.Policy, error) {
	return retentionModel.DefaultPolicy(), nil
}

// The SQL client name is the Go one.
func TestLabClientNameSQLMatchesGo(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	for i := 0; i < 20; i++ {
		id := uuid.Must(uuid.NewV7())
		if i == 0 {
			id = uuid.Nil
		}
		var name string
		require.NoError(t, db.Pool.QueryRow(context.Background(), `SELECT lab_client_name($1)`, id).Scan(&name))
		require.Equal(t, labBindingModel.ParticipantClientName(id), name)
	}
}

func erasurePayload(client, ip string) string {
	return `{"clients":[{"name":"` + client + `","status":{"assignedIp":"` + ip + `","statistics":{"rxBytes":"5"}}}],` +
		`"policies":[{"rules":[{"clientNames":["` + client + `"]}]}]}`
}

// Deleting an account removes only the link to the person: the VPN client name becomes one pseudonym, the same in
// the minute tables, the journal, the raw rows, the current state and the archive; IPs and traffic stay. What the
// telemetry brings in later with the real name (raw rows archived after the account went) is rewritten too.
func TestAccountDeletionErasesLabTelemetry(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "laberase")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	gone := labBindingModel.ParticipantClientName(f.user)
	other := labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7()))
	at := anStart.Add(time.Hour)

	for _, name := range []string{gone, other} {
		rtExec(t, db, `INSERT INTO event_vpn_client_minutes (event_id, team_id, lab_group_name, client_name, bucket_at, samples, connected_samples, rx_bytes, tx_bytes, last_rx, last_tx, last_observed_at)
VALUES ($1, $2, 'g', $3, $4, 1, 1, 10, 1, 10, 1, $4)`, f.event, f.team, name, at)
		rtExec(t, db, `INSERT INTO event_lab_transitions (id, event_id, team_id, lab_group_name, client_name, kind, at, received_at)
VALUES ($1, $2, $3, 'g', $4, 'client_connected', $5, $5)`, uuid.Must(uuid.NewV7()), f.event, f.team, name, at)
		rtExec(t, db, `INSERT INTO event_lab_journal_states (event_id, team_id, lab_group_name, kind, lab_name, name, state)
VALUES ($1, $2, 'g', 'client', '', $3, '{}')`, f.event, f.team, name)
	}
	telemetryObservation(t, db, f.event, f.team, 1, at, erasurePayload(gone, "10.8.0.7"))
	telemetryObservation(t, db, f.event, f.team, 2, at.Add(time.Second), erasurePayload(other, "10.8.0.9"))
	rtExec(t, db, `INSERT INTO lab_monitoring_current (event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, updated_at, payload)
VALUES ($1, $2, 'grp', 'agent', 1, $3, $3, $4)`, f.event, f.team, at, erasurePayload(gone, "10.8.0.7"))

	line := func(client, ip string) string {
		row, _ := json.Marshal(map[string]any{
			"id": uuid.Must(uuid.NewV7()), "event_id": f.event, "event_team_id": f.team, "lab_group_name": "g", "agent_id": "a",
			"sequence": 1, "observed_at": at, "received_at": at, "schema_version": 3, "snapshot": false,
			"payload": json.RawMessage(erasurePayload(client, ip)),
		})
		return string(row)
	}
	data, err := eventAnalyticsModel.CompressLabArchive([]string{line(gone, "10.8.0.7"), line(other, "10.8.0.9")})
	require.NoError(t, err)
	chunk := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_lab_observation_archives (id, event_id, period_start, period_end, row_count, first_received_at, first_id, last_received_at, last_id, codec, data)
VALUES ($1, $2, $3, $4, 2, $3, $1, $3, $1, $5, $6)`, chunk, f.event, at, at.Add(time.Hour), eventAnalyticsModel.LabArchiveCodecZstdNDJSON, data)

	// The account is deleted and its personal data purged.
	users := userRepo.New(db.Queries)
	u, err := users.GetByID(ctx, f.user)
	require.NoError(t, err)
	expected := u.UpdatedAt
	require.NoError(t, u.SoftDelete(anStart))
	_, err = users.Update(ctx, u, expected)
	require.NoError(t, err)
	store := retentionRepo.New(db.Queries)
	purged, err := store.PurgeDeletedAccounts(ctx, anStart, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, purged)

	var pseudonym string
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT pseudonym FROM lab_telemetry_erasures WHERE user_id = $1 AND $2 = ANY (event_ids)`, f.user, f.event).Scan(&pseudonym),
		"queued with the event it took part in")
	require.True(t, strings.HasPrefix(pseudonym, "deleted-"))

	// Everything the person's name was in carries the same pseudonym at once, the traffic and IPs stay.
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_vpn_client_minutes WHERE client_name = $1`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_vpn_client_minutes WHERE client_name = $1 AND rx_bytes = 10`, pseudonym))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE client_name = $1 AND kind = 'client_connected'`, pseudonym))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE client_name = $1`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_journal_states WHERE kind = 'client' AND name = $1`, pseudonym))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_journal_states WHERE name = $1`, gone))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE payload::text LIKE '%' || $1 || '%'`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE payload::text LIKE '%' || $1 || '%' AND payload::text LIKE '%10.8.0.7%'`, pseudonym))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM lab_monitoring_current WHERE payload::text LIKE '%' || $1 || '%'`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM lab_monitoring_current WHERE payload::text LIKE '%' || $1 || '%' AND payload::text LIKE '%10.8.0.7%'`, pseudonym))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE client_name = $1`, other), "the other participant stays")

	// The archive is rewritten by the retention job; a raw row that still carries the real name and is archived
	// after the account went must not bring it back.
	telemetryObservation(t, db, f.event, f.team, 3, at.Add(2*time.Second), erasurePayload(gone, "10.8.0.7"))
	for cursor := (eventAnalyticsRepo.VPNCursor{}); ; {
		next, rows, aerr := eventAnalyticsRepo.New(db.Queries).ArchiveLabObservations(ctx, f.event, cursor, anStart.AddDate(1, 0, 0), uuid.Must(uuid.NewV7()))
		require.NoError(t, aerr)
		if rows == 0 {
			break
		}
		cursor = next
	}
	uc := retention.New(retention.Dependencies{Store: store, Policies: fixedPolicy{}, Now: func() time.Time { return anStart }})
	for pass := 0; pass < 2; pass++ { // a second run changes nothing
		require.NoError(t, uc.EnforceDataRetention(ctx))
		var all []string
		rows, qerr := db.Pool.Query(ctx, `SELECT data FROM event_lab_observation_archives WHERE event_id = $1`, f.event)
		require.NoError(t, qerr)
		for rows.Next() {
			var stored []byte
			require.NoError(t, rows.Scan(&stored))
			require.NoError(t, eventAnalyticsModel.LabArchiveLines(eventAnalyticsModel.LabArchiveCodecZstdNDJSON, stored, func(l []byte) error {
				all = append(all, string(l))
				return nil
			}))
		}
		rows.Close()
		text := strings.Join(all, "\n")
		require.GreaterOrEqual(t, len(all), 3)
		require.NotContains(t, text, gone)
		require.Contains(t, text, pseudonym)
		require.Contains(t, text, "10.8.0.7", "the IPs stay")
		require.Contains(t, text, other, "the other participant stays")
		require.Contains(t, text, "10.8.0.9")
		require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM lab_telemetry_erasures`), "still queued: the day has not passed")
	}
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE payload::text LIKE '%' || $1 || '%'`, gone))

	// A day after the purge, with the archive past every raw row of that day, the entry settles.
	rtExec(t, db, `UPDATE lab_telemetry_erasures SET queued_at = $1`, anStart.Add(-48*time.Hour))
	rtExec(t, db, `UPDATE event_analytics_rollups SET lab_archive_cursor_received_at = 'infinity'::timestamptz WHERE event_id = $1`, f.event)
	require.NoError(t, uc.EnforceDataRetention(ctx))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM lab_telemetry_erasures`), "the queue is drained")
}

// The latest snapshot of a lab group is not kept for good: the rolled-up purge leaves it, but the telemetry period
// removes it (once archived) like every other raw row.
func TestRetention_LatestRawSnapshotGoesWithTheTelemetryPeriod(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "latestraw")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	store := retentionRepo.New(db.Queries)
	at := anStart.Add(time.Hour)
	telemetryObservation(t, db, f.event, f.team, 1, at, erasurePayload("p-x", "10.8.0.1")) // the only (so the latest) snapshot of its group
	cut := at.AddDate(0, 0, 1)

	n, err := store.PurgeEventLabObservations(ctx, cut, 100)
	require.NoError(t, err)
	require.Zero(t, n, "not archived yet")

	for cursor := (eventAnalyticsRepo.VPNCursor{}); ; {
		next, rows, aerr := eventAnalyticsRepo.New(db.Queries).ArchiveLabObservations(ctx, f.event, cursor, cut, uuid.Must(uuid.NewV7()))
		require.NoError(t, aerr)
		if rows == 0 {
			break
		}
		cursor = next
	}
	n, err = store.PurgeEventLabObservations(ctx, cut, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE event_id = $1`, f.event))
}
