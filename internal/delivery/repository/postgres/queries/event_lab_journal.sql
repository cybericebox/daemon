-- The transitions journal of the lab telemetry (docs/LAB-TRAFFIC-ACCOUNTING.md):
-- state changes seen between consecutive observations of an event, found by
-- the analytics job in arrival order behind its own cursor.

-- name: ListEventLabJournalStates :many
-- The last known state of every entity of the event.
SELECT team_id, lab_group_name, kind, lab_name, name, state
FROM event_lab_journal_states
WHERE event_id = sqlc.arg(event_id);

-- name: SaveEventLabJournalBatch :exec
-- Stores the transitions and the changed entity states of one batch of
-- observations and moves the journal cursor past it, in one statement: a
-- failure leaves all three untouched. transitions and states are JSON arrays. Nothing is stored while the event
-- is held (a recompute runs) unless ignore_hold is set (the recompute itself).
WITH allowed AS (
    SELECT 1 AS ok
    WHERE sqlc.arg(ignore_hold)::boolean
       OR NOT EXISTS (SELECT 1 FROM event_analytics_rollups h
                      WHERE h.event_id = sqlc.arg(event_id) AND h.lab_telemetry_hold_until > now())
), inserted AS (
    INSERT INTO event_lab_transitions (id, event_id, team_id, lab_group_name, lab_name, device_name, client_name,
                                       kind, at, received_at, detail)
    SELECT x.id, sqlc.arg(event_id), x.team_id, x.lab_group_name, x.lab_name, x.device_name, x.client_name,
           x.kind, x.at, x.received_at, x.detail
    FROM allowed, jsonb_to_recordset(sqlc.arg(transitions)::jsonb)
             AS x(id uuid, team_id uuid, lab_group_name text, lab_name text, device_name text, client_name text,
                  kind text, at timestamptz, received_at timestamptz, detail text)
    RETURNING 1
), states AS (
    INSERT INTO event_lab_journal_states (event_id, team_id, lab_group_name, kind, lab_name, name, state)
    SELECT sqlc.arg(event_id), x.team_id, x.lab_group_name, x.kind, x.lab_name, x.name, x.state
    FROM allowed, jsonb_to_recordset(sqlc.arg(states)::jsonb)
             AS x(team_id uuid, lab_group_name text, kind text, lab_name text, name text, state jsonb)
    ON CONFLICT (event_id, team_id, lab_group_name, kind, lab_name, name) DO UPDATE
        SET state = EXCLUDED.state
    RETURNING 1
)
INSERT INTO event_analytics_rollups (event_id, lab_journal_cursor_received_at, lab_journal_cursor_id)
SELECT sqlc.arg(event_id), sqlc.arg(cursor_received_at), sqlc.arg(cursor_id) FROM allowed
ON CONFLICT (event_id) DO UPDATE
    SET lab_journal_cursor_received_at = EXCLUDED.lab_journal_cursor_received_at,
        lab_journal_cursor_id          = EXCLUDED.lab_journal_cursor_id,
        lab_usage_finalized_at         = NULL;
