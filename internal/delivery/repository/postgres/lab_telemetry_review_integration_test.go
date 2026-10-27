package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole repository
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/retention"
)

func archiveUpTo(t *testing.T, repo *eventAnalyticsRepo.Repository, event uuid.UUID, closedBefore time.Time) eventAnalyticsRepo.VPNCursor {
	t.Helper()
	var cursor eventAnalyticsRepo.VPNCursor
	for {
		next, rows, err := repo.ArchiveLabObservations(context.Background(), event, cursor, closedBefore, uuid.Must(uuid.NewV7()))
		require.NoError(t, err)
		if rows == 0 {
			return cursor
		}
		cursor = next
	}
}

// A raw row committed behind the archive cursor is in no chunk: it is not deleted, the retention run archives it in a
// chunk of its own, and only then it may go.
func TestRetention_LateRawRowIsArchivedBeforeItCanGo(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "laterow")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	repo := eventAnalyticsRepo.New(db.Queries)
	store := retentionRepo.New(db.Queries)
	at := anStart.Add(time.Hour)
	telemetryObservation(t, db, f.event, f.team, 1, at, erasurePayload("p-a", "10.8.0.1"))
	telemetryObservation(t, db, f.event, f.team, 2, at.Add(30*time.Minute), erasurePayload("p-a", "10.8.0.1"))
	cursor := archiveUpTo(t, repo, f.event, anStart.AddDate(0, 0, 1))
	require.NotEqual(t, uuid.Nil, cursor.ID)

	// The late one: received_at far behind the cursor, committed after the chunk was written.
	telemetryObservation(t, db, f.event, f.team, 3, at.Add(5*time.Minute), erasurePayload("p-b", "10.8.0.2"))
	// Every rollup passed everything (so only the archive stands between the rows and the purge).
	rtExec(t, db, `UPDATE event_analytics_rollups SET lab_usage_cursor_received_at = $2, lab_usage_cursor_id = $3, lab_journal_cursor_received_at = $2,
 lab_journal_cursor_id = $3, vpn_cursor_received_at = $2, vpn_cursor_id = $3 WHERE event_id = $1`, f.event, cursor.ReceivedAt, cursor.ID)
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = false WHERE id = $1`, f.event)

	cutoff := anStart.AddDate(0, 0, 3)
	n, err := store.PurgeRolledUpEventLabObservations(ctx, cutoff, 100)
	require.NoError(t, err)
	// The latest of the group stays; the first one is archived and goes; the late one is not in a chunk and stays.
	require.EqualValues(t, 1, n)
	require.Equal(t, 2, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE event_id = $1`, f.event))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE event_id = $1 AND payload::text LIKE '%p-b%'`, f.event))

	uc := retention.New(retention.Dependencies{Store: store, Policies: fixedPolicy{}, Now: func() time.Time { return anStart.AddDate(0, 0, 3) }})
	require.NoError(t, uc.EnforceDataRetention(ctx))
	require.Equal(t, 3, rtCount(t, db, `SELECT count(*) FROM event_lab_archived_rows ar JOIN event_lab_observation_archives a ON a.id = ar.chunk_id WHERE a.event_id = $1`, f.event),
		"the late row has a chunk of its own now")
	require.Equal(t, 2, rtCount(t, db, `SELECT count(*) FROM event_lab_observation_archives WHERE event_id = $1`, f.event))
}

// While an event is held (a recompute runs) the rollup statements read nothing, unless they are the recompute's.
func TestEventLabTelemetry_HoldStopsTheRollupStatements(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "labhold")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	at := anStart.Add(time.Hour)
	telemetryObservation(t, db, f.event, f.team, 1, at, telemetryPayload(true, "Running", 10, 1000, "p-a", at, 1, 1))
	until := time.Now().Add(time.Hour)
	require.NoError(t, repo.HoldLabTelemetry(ctx, f.event, &until))

	_, read, err := repo.RollupLabUsage(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 10, false)
	require.NoError(t, err)
	require.Zero(t, read, "held: nothing is read")
	journal, err := repo.LabJournal(ctx, f.event, func() uuid.UUID { return uuid.Must(uuid.NewV7()) })
	require.NoError(t, err)
	_, _, err = repo.RollupLabJournal(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 10, journal, false)
	require.NoError(t, err)
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_lab_transitions WHERE event_id = $1`, f.event), "held: nothing is stored")
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_analytics_rollups WHERE lab_journal_cursor_id IS NOT NULL`))

	_, read, err = repo.RollupLabUsage(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 10, true)
	require.NoError(t, err)
	require.Equal(t, 1, read, "the recompute reads through the hold")
}

// An unreadable counter skips the sample (it is not a zero that would show as a reset), a sample older than the
// newest one stored is left out, and the base of the deltas is the newest sample stored, not the last minute.
func TestEventLabTelemetry_TrafficIgnoresBadAndStaleSamples(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "labtraffic")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	m0 := anStart.Add(time.Hour).UTC()
	client := func(rx, tx string) string {
		return `{"clients":[{"name":"p-a","status":{"statistics":{"rxBytes":` + rx + `,"txBytes":` + tx + `}}}]}`
	}
	telemetryObservation(t, db, f.event, f.team, 1, m0.Add(1*time.Second), client(`"1000"`, `"100"`))
	telemetryObservation(t, db, f.event, f.team, 2, m0.Add(2*time.Second), client(`"-5"`, `"abc"`)) // unreadable: skipped
	telemetryObservation(t, db, f.event, f.team, 3, m0.Add(3*time.Second), client(`"1500"`, `"150"`))
	cursor, _, err := repo.RollupLabUsage(ctx, f.event, eventAnalyticsRepo.VPNCursor{}, 10, false)
	require.NoError(t, err)

	// Another batch: a late sample from before the newest one stored, then a fresh one.
	telemetryObservation(t, db, f.event, f.team, 4, m0.Add(1500*time.Millisecond), client(`"50"`, `"5"`))
	telemetryObservation(t, db, f.event, f.team, 5, m0.Add(4*time.Second), client(`"1700"`, `"170"`))
	_, _, err = repo.RollupLabUsage(ctx, f.event, cursor, 10, false)
	require.NoError(t, err)

	var samples, rx, tx, lastRx int64
	require.NoError(t, db.Pool.QueryRow(ctx,
		`SELECT sum(samples), sum(rx_bytes), sum(tx_bytes), max(last_rx) FROM event_vpn_client_minutes WHERE event_id = $1`, f.event).
		Scan(&samples, &rx, &tx, &lastRx))
	require.EqualValues(t, 3, samples, "the unreadable and the stale samples are not counted")
	require.EqualValues(t, 700, rx, "first sample no baseline, +500, +200: no false reset")
	require.EqualValues(t, 70, tx)
	require.EqualValues(t, 1700, lastRx)
}

// A client name is renamed with the same drop-or-rename care in the account purge path: a leftover row with the real
// name in a minute that already has the pseudonymous twin must not fail the whole run.
func TestAccountDeletion_RenameCollisionDoesNotFailTheRun(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "labcollide")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	gone := labBindingModel.ParticipantClientName(f.user)
	at := anStart.Add(time.Hour)
	store := retentionRepo.New(db.Queries)

	// Erased on request first (the pseudonym exists), then a rollup wrote a row under the real name in the same minute.
	entry, err := store.QueueLabTelemetryErasure(ctx, f.user)
	require.NoError(t, err)
	for _, name := range []string{entry.Pseudonym, gone} {
		rtExec(t, db, `INSERT INTO event_vpn_client_minutes (event_id, team_id, lab_group_name, client_name, bucket_at, samples, connected_samples, rx_bytes, tx_bytes, last_rx, last_tx, last_observed_at)
VALUES ($1, $2, 'g', $3, $4, 1, 1, 1, 1, 1, 1, $4)`, f.event, f.team, name, at)
		rtExec(t, db, `INSERT INTO event_lab_journal_states (event_id, team_id, lab_group_name, kind, lab_name, name, state)
VALUES ($1, $2, 'g', 'client', '', $3, '{}')`, f.event, f.team, name)
	}
	uc := retention.New(retention.Dependencies{Store: store, Policies: fixedPolicy{}, Now: func() time.Time { return time.Now().UTC() }})
	require.NoError(t, uc.EnforceDataRetention(ctx))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_vpn_client_minutes WHERE event_id = $1`, f.event))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM event_vpn_client_minutes WHERE client_name = $1`, gone))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_journal_states WHERE kind = 'client'`))
}

// A chunk that cannot be read is logged and skipped: the others are still scrubbed, the entry is not closed on the
// quiet (it could still hold a name) and closes once it is past the forced age. A chunk changed by someone else
// between read and write is not overwritten.
func TestLabTelemetryErasure_BadChunkDoesNotStopTheRest(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "labbadchunk")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	gone := labBindingModel.ParticipantClientName(f.user)
	now := time.Now().UTC()
	at := now.Add(-time.Hour)
	good := erasureArchiveChunk(t, db, f.event, f.team, at, at, gone, "10.8.0.7")
	bad := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_lab_observation_archives (id, event_id, period_start, period_end, row_count, first_received_at, first_id, last_received_at, last_id, codec, data)
VALUES ($1, $2, $3, $4, 1, $3, $1, $3, $1, 'zstd-ndjson-v1', '\x0102030405')`, bad, f.event, at.Add(-time.Hour), at)

	store := retentionRepo.New(db.Queries)
	uc := retention.New(retention.Dependencies{Store: store, Policies: fixedPolicy{}, Now: func() time.Time { return now }})
	entry, err := store.QueueLabTelemetryErasure(ctx, f.user)
	require.NoError(t, err)
	require.NoError(t, uc.EnforceDataRetention(ctx))
	require.NotContains(t, archiveText(t, db, good), gone, "the readable chunk is scrubbed")
	require.Contains(t, archiveText(t, db, good), entry.Pseudonym)

	// Old enough to settle by the clock, but a chunk failed in this run: it stays until the forced age.
	rtExec(t, db, `UPDATE lab_telemetry_erasures SET queued_at = $1`, now.Add(-3*24*time.Hour))
	require.NoError(t, uc.EnforceDataRetention(ctx))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM lab_telemetry_erasures`))
	rtExec(t, db, `UPDATE lab_telemetry_erasures SET queued_at = $1`, now.Add(-8*24*time.Hour))
	require.NoError(t, uc.EnforceDataRetention(ctx))
	require.Zero(t, rtCount(t, db, `SELECT count(*) FROM lab_telemetry_erasures`), "forced closed")

	// A write based on stale data does not go through.
	var current []byte
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT data FROM event_lab_observation_archives WHERE id = $1`, good).Scan(&current))
	saved, err := store.SaveScrubbedLabArchiveChunk(ctx, good, []byte("not what is stored"), []byte("x"))
	require.NoError(t, err)
	require.False(t, saved)
	saved, err = store.SaveScrubbedLabArchiveChunk(ctx, good, current, current)
	require.NoError(t, err)
	require.True(t, saved)
}

// Erasure on request blanks the person's IP only in rows that name the person: an address another client got later
// (the pool reuses addresses) is left alone.
func TestLabTelemetryErasureOnRequest_ReusedIPOfAnotherClientStays(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "labipreuse")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	gone := labBindingModel.ParticipantClientName(f.user)
	other := labBindingModel.ParticipantClientName(uuid.Must(uuid.NewV7()))
	at := anStart.Add(time.Hour)
	telemetryObservation(t, db, f.event, f.team, 1, at, erasurePayload(gone, "10.8.0.7"))
	telemetryObservation(t, db, f.event, f.team, 2, at.Add(time.Hour), erasurePayload(other, "10.8.0.7")) // the address reused
	chunkGone := erasureArchiveChunk(t, db, f.event, f.team, at, at, gone, "10.8.0.7")
	chunkOther := erasureArchiveChunk(t, db, f.event, f.team, at.Add(time.Hour), at, other, "10.8.0.7")

	uc := retention.New(retention.Dependencies{Store: retentionRepo.New(db.Queries), Now: func() time.Time { return anStart }})
	_, _, err := uc.EraseLabTelemetryOf(ctx, f.user)
	require.NoError(t, err)

	require.NotContains(t, archiveText(t, db, chunkGone), `"10.8.0.7"`)
	require.Contains(t, archiveText(t, db, chunkOther), `"10.8.0.7"`, "another client's row is not touched")
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE payload::text LIKE '%"10.8.0.7"%'`))
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM event_lab_observations WHERE payload::text LIKE '%' || $1 || '%' AND payload::text LIKE '%"10.8.0.7"%'`, other))
}

// Raw rows past their period that are still there are counted, so the retention run can report them.
func TestRetention_StaleRawRowsAreCounted(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	f := anSeed(t, db, "labstale")
	store := retentionRepo.New(db.Queries)
	at := anStart.Add(time.Hour)
	telemetryObservation(t, db, f.event, f.team, 1, at, erasurePayload("p-a", "10.8.0.1"))
	n, err := store.StaleLabObservations(context.Background(), at.Add(time.Hour))
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	n, err = store.StaleLabObservations(context.Background(), at.Add(-time.Hour))
	require.NoError(t, err)
	require.Zero(t, n)
}

// Collecting the person's IPs fails closed: an archive row that names the person but cannot be read leaves the
// erasure open (an error, the entry stays); the readable chunks are still scrubbed.
func TestLabTelemetryErasureOnRequest_UnreadableRowKeepsTheEntryOpen(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "labfailclosed")
	rtExec(t, db, `UPDATE events SET infrastructure_allowed = true WHERE id = $1`, f.event)
	gone := labBindingModel.ParticipantClientName(f.user)
	at := anStart.Add(time.Hour)
	good := erasureArchiveChunk(t, db, f.event, f.team, at, at, gone, "10.8.0.7")

	data, err := eventAnalyticsModel.CompressLabArchive([]string{`{"payload":"` + gone + `"}`})
	require.NoError(t, err)
	odd := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_lab_observation_archives (id, event_id, period_start, period_end, row_count, first_received_at, first_id, last_received_at, last_id, codec, data)
VALUES ($1, $2, $3, $4, 1, $3, $1, $3, $1, $5, $6)`, odd, f.event, at.Add(-time.Hour), at, eventAnalyticsModel.LabArchiveCodecZstdNDJSON, data)

	store := retentionRepo.New(db.Queries)
	uc := retention.New(retention.Dependencies{Store: store, Now: func() time.Time { return anStart }})
	_, _, err = uc.EraseLabTelemetryOf(ctx, f.user)
	require.Error(t, err, "an incomplete erasure is reported")
	require.Equal(t, 1, rtCount(t, db, `SELECT count(*) FROM lab_telemetry_erasures`), "and stays in the queue")
	require.NotContains(t, archiveText(t, db, good), gone, "what could be done was done")
}
