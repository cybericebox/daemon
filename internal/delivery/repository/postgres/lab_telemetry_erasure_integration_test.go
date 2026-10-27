package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

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

// Deleting an account takes the participant out of the minute tables, the journal and the archive: the client
// name and the assigned IP go, the other participants and the team's traffic stay.
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

	line := func(client, ip string) string {
		payload, _ := json.Marshal(map[string]any{
			"clients":  []any{map[string]any{"name": client, "status": map[string]any{"assignedIp": ip, "statistics": map[string]any{"rxBytes": "5"}}}},
			"policies": []any{map[string]any{"rules": []any{map[string]any{"clientNames": []string{client}}}}},
		})
		row, _ := json.Marshal(map[string]any{
			"id": uuid.Must(uuid.NewV7()), "event_id": f.event, "event_team_id": f.team, "lab_group_name": "g", "agent_id": "a",
			"sequence": 1, "observed_at": at, "received_at": at, "schema_version": 3, "snapshot": false, "payload": json.RawMessage(payload),
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

	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_vpn_client_minutes WHERE client_name = $1`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_vpn_client_minutes WHERE client_name LIKE 'deleted-%' AND rx_bytes = 10`), "the traffic stays, unnamed")
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_vpn_client_minutes WHERE client_name = $1`, other))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE client_name = $1`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE kind = 'client_connected' AND client_name IS NULL`))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE client_name = $1`, other))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_journal_states WHERE name = $1`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_journal_states WHERE name = $1`, other))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM lab_telemetry_erasures WHERE user_id = $1 AND $2 = ANY (event_ids)`, f.user, f.event),
		"queued with the event it took part in")

	// The archive is still untouched until the retention job scrubs it.
	var stored []byte
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT data FROM event_lab_observation_archives WHERE id = $1`, chunk).Scan(&stored))
	require.Equal(t, data, stored)

	uc := retention.New(retention.Dependencies{Store: store, Policies: fixedPolicy{}, Now: func() time.Time { return anStart }})
	for pass := 0; pass < 2; pass++ { // a second run changes nothing
		require.NoError(t, uc.EnforceDataRetention(ctx))
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT data FROM event_lab_observation_archives WHERE id = $1`, chunk).Scan(&stored))
		var all []string
		require.NoError(t, eventAnalyticsModel.LabArchiveLines(eventAnalyticsModel.LabArchiveCodecZstdNDJSON, stored, func(l []byte) error {
			all = append(all, string(l))
			return nil
		}))
		text := strings.Join(all, "\n")
		require.Len(t, all, 2)
		require.NotContains(t, text, gone)
		require.NotContains(t, text, "10.8.0.7")
		require.Contains(t, text, eventAnalyticsModel.LabArchiveErasedClient)
		require.Contains(t, text, other, "the other participant stays")
		require.Contains(t, text, "10.8.0.9")
		require.Zero(t, rtCount(t, db, `SELECT count(*) FROM lab_telemetry_erasures`), "the queue is drained")
	}
}
