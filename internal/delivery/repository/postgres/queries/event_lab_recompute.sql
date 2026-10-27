-- Recompute of an event's lab telemetry rollups from the archive
-- (docs/LAB-TRAFFIC-ACCOUNTING.md): a hold that keeps the jobs away, a reset
-- of the derived tables and cursors, and a restore of archived raw rows.

-- name: SetEventLabTelemetryHold :exec
-- Holds the event's lab telemetry rollups and raw purge until hold_until; a
-- null hold_until releases it.
INSERT INTO event_analytics_rollups (event_id, lab_telemetry_hold_until)
VALUES (sqlc.arg(event_id), sqlc.narg(hold_until))
ON CONFLICT (event_id) DO UPDATE
    SET lab_telemetry_hold_until = EXCLUDED.lab_telemetry_hold_until;

-- name: ResetEventLabTelemetry :exec
-- Deletes everything the lab usage rollup and the transitions journal derived
-- for the event and rewinds their cursors; the VPN sessions, the archive and
-- its cursor stay.
WITH usage AS (
    DELETE FROM event_lab_usage_minutes WHERE event_id = sqlc.arg(event_id) RETURNING 1
), labs AS (
    DELETE FROM event_lab_minutes WHERE event_id = sqlc.arg(event_id) RETURNING 1
), clients AS (
    DELETE FROM event_vpn_client_minutes WHERE event_id = sqlc.arg(event_id) RETURNING 1
), transitions AS (
    DELETE FROM event_lab_transitions WHERE event_id = sqlc.arg(event_id) RETURNING 1
), states AS (
    DELETE FROM event_lab_journal_states WHERE event_id = sqlc.arg(event_id) RETURNING 1
)
UPDATE event_analytics_rollups
SET lab_usage_cursor_received_at   = NULL,
    lab_usage_cursor_id            = NULL,
    lab_usage_finalized_at         = NULL,
    lab_journal_cursor_received_at = NULL,
    lab_journal_cursor_id          = NULL
WHERE event_analytics_rollups.event_id = sqlc.arg(event_id);

-- name: RestoreEventLabObservations :execrows
-- Puts archived raw rows (JSON objects as the archive holds them) back into
-- the raw table; rows that are there already are left alone.
INSERT INTO event_lab_observations (id, event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at,
                                    received_at, schema_version, snapshot, payload)
SELECT r.id, r.event_id, r.event_team_id, r.lab_group_name, r.agent_id, r.sequence, r.observed_at,
       r.received_at, r.schema_version, r.snapshot, r.payload
FROM jsonb_populate_recordset(NULL::event_lab_observations, sqlc.arg(observations)::jsonb) AS r
ON CONFLICT DO NOTHING;

-- name: ListEventIDsByTag :many
SELECT id FROM events WHERE tag = sqlc.arg(tag) ORDER BY created_at, id;

-- name: GetEventLabTelemetryCoverage :one
-- Where the event's derived lab telemetry starts and where the sources a recompute can rebuild it from (the archive
-- and the raw rows) start, in unix seconds; -1: there is none. Data older than its sources cannot be rebuilt. Minutes are
-- compared by their bucket and transitions by the platform time of the observation they came from (received_at),
-- not by the agent time in `at`: a pod queued long before the first observation is no gap.
SELECT COALESCE(extract(epoch FROM LEAST(
           (SELECT min(m.bucket_at) FROM event_lab_usage_minutes m WHERE m.event_id = sqlc.arg(event_id)),
           (SELECT min(m.bucket_at) FROM event_lab_minutes m WHERE m.event_id = sqlc.arg(event_id)),
           (SELECT min(m.bucket_at) FROM event_vpn_client_minutes m WHERE m.event_id = sqlc.arg(event_id)),
           (SELECT min(t.received_at) FROM event_lab_transitions t WHERE t.event_id = sqlc.arg(event_id))))::bigint, -1)::bigint AS data_from,
       COALESCE(extract(epoch FROM LEAST(
           (SELECT min(a.first_received_at) FROM event_lab_observation_archives a WHERE a.event_id = sqlc.arg(event_id)),
           (SELECT min(o.received_at) FROM event_lab_observations o WHERE o.event_id = sqlc.arg(event_id))))::bigint, -1)::bigint AS covered_from;
