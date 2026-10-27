package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// archiveAll archives chunks until nothing is left before closedBefore and
// returns how many chunks it wrote.
func archiveAll(
	t *testing.T, repo *eventAnalyticsRepo.Repository, event uuid.UUID, from eventAnalyticsRepo.VPNCursor, closedBefore time.Time,
) (int, eventAnalyticsRepo.VPNCursor) {
	t.Helper()
	cursor, chunks := from, 0
	for {
		next, rows, err := repo.ArchiveLabObservations(
			context.Background(), event, cursor, closedBefore, uuid.Must(uuid.NewV7()),
		)
		require.NoError(t, err)
		if rows == 0 {
			return chunks, cursor
		}
		chunks++
		cursor = next
	}
}

// The raw observations are archived by closed hour, every column survives the
// round trip, a second run adds nothing, and the hour still open is left.
func TestEventLabArchive_RoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "labarchive")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)

	// Three hours of observations of two lab groups, plus one in the open hour.
	hour := anStart
	want := map[uuid.UUID]string{}
	seq := 0
	for h := 0; h < 3; h++ {
		for i := 0; i < 30; i++ {
			for _, group := range []string{"group-b", "group-a"} {
				seq++
				id := uuid.Must(uuid.NewV7())
				at := hour.Add(time.Duration(h)*time.Hour + time.Duration(i)*time.Minute)
				payload := fmt.Sprintf(`{"agentId":"agent","sequence":"%d","labs":[{"name":"web","status":{"phase":"Ready"}}],"note":"ж %d"}`, seq, i)
				rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, $4, 'agent', $5, $6, $7, 3, $8, $9)`,
					id, f.event, f.team, group, seq, at.Add(-2*time.Second), at, i == 0, payload)
				want[id] = payload
			}
		}
	}
	open := uuid.Must(uuid.NewV7())
	rtExec(t, db, `
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload)
VALUES ($1, $2, $3, 'group-a', 'agent', 1000, $4, $4, 3, false, '{}')`, open, f.event, f.team, hour.Add(3*time.Hour+10*time.Minute))

	closedBefore := hour.Add(3 * time.Hour)
	chunks, cursor := archiveAll(t, repo, f.event, eventAnalyticsRepo.VPNCursor{}, closedBefore)
	require.Equal(t, 3, chunks, "one chunk per closed hour")
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE id = '`+open.String()+`'`))
	again, _ := archiveAll(t, repo, f.event, cursor, closedBefore)
	require.Zero(t, again, "a second run archives nothing")

	var rows int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT count(*), COALESCE(sum(row_count), 0) FROM event_lab_observation_archives WHERE event_id = $1`, f.event).
		Scan(new(int), &rows))
	require.Equal(t, len(want), rows)

	// Decode every chunk: all columns are back, grouped by lab group and time.
	got := 0
	var after eventAnalyticsRepo.VPNCursor
	for {
		page, err := repo.LabArchiveChunks(ctx, f.event, after, 2)
		require.NoError(t, err)
		if len(page) == 0 {
			break
		}
		for _, chunk := range page {
			require.Equal(t, eventAnalyticsModel.LabArchiveCodecZstdNDJSON, chunk.Codec)
			require.Equal(t, time.Hour, chunk.PeriodEnd.Sub(chunk.PeriodStart))
			var lastGroup string
			var lastObserved time.Time
			require.NoError(t, eventAnalyticsModel.DecodeLabArchive(chunk.Codec, chunk.Data, func(o eventAnalyticsModel.LabObservation) error {
				payload, ok := want[o.ID]
				require.True(t, ok, "unknown row %s", o.ID)
				require.Equal(t, f.event, o.EventID)
				require.Equal(t, f.team, o.EventTeamID)
				require.Equal(t, "agent", o.AgentID)
				require.EqualValues(t, 3, o.SchemaVersion)
				require.Equal(t, o.ObservedAt.Add(2*time.Second).UTC(), o.ReceivedAt.UTC())
				require.JSONEq(t, payload, string(o.Payload))
				require.Equal(t, o.Snapshot, o.ReceivedAt.Minute() == 0)
				require.GreaterOrEqual(t, o.LabGroupName, lastGroup)
				if o.LabGroupName == lastGroup {
					require.False(t, o.ObservedAt.Before(lastObserved))
				}
				lastGroup, lastObserved = o.LabGroupName, o.ObservedAt
				delete(want, o.ID)
				got++
				return nil
			}))
			after = chunk.Last
		}
	}
	require.Equal(t, 180, got)
	require.Empty(t, want)
	require.Equal(t, cursor, after)

	// The compressed chunk is small, and the rollup row carries the cursor.
	var size int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT sum(octet_length(data)) FROM event_lab_observation_archives WHERE event_id = $1`, f.event).Scan(&size))
	var raw int
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT sum(octet_length(payload::text)) FROM event_lab_observations WHERE event_id = $1`, f.event).Scan(&raw))
	require.Less(t, size, raw)

	// The candidate listing sees an open archive only while rows remain.
	cands, err := repo.RollupCandidates(ctx, hour.Add(3*time.Hour+30*time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, cursor, cands[0].ArchiveCursor)
	require.False(t, cands[0].ArchiveOpen, "the open hour is not due yet")
	cands, err = repo.RollupCandidates(ctx, hour.Add(5*time.Hour), 10)
	require.NoError(t, err)
	require.True(t, cands[0].ArchiveOpen)
}

func TestLabArchive_ChunkRowLimitAndCodec(t *testing.T) {
	line := func(i int) string {
		b, _ := json.Marshal(map[string]any{
			"id": uuid.Must(uuid.NewV7()), "event_id": uuid.Nil, "event_team_id": uuid.Nil, "lab_group_name": "g",
			"agent_id": "a", "sequence": i, "observed_at": time.Unix(int64(i), 0).UTC(),
			"received_at": time.Unix(int64(i), 0).UTC(), "schema_version": 3, "snapshot": false, "payload": map[string]any{"i": i},
		})
		return string(b)
	}
	data, err := eventAnalyticsModel.CompressLabArchive([]string{line(1), line(2)})
	require.NoError(t, err)
	var seen []int64
	require.NoError(t, eventAnalyticsModel.DecodeLabArchive(eventAnalyticsModel.LabArchiveCodecZstdNDJSON, data,
		func(o eventAnalyticsModel.LabObservation) error { seen = append(seen, o.Sequence); return nil }))
	require.Equal(t, []int64{1, 2}, seen)
	require.Error(t, eventAnalyticsModel.DecodeLabArchive("gzip", data, nil))
}
