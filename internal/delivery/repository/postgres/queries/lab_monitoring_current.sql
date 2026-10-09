-- name: FindEventTeamsByLabGroup :many
-- Every event team bound to the lab group, whatever the event lifecycle: the
-- current state must already be there when an event becomes active.
SELECT DISTINCT lb.event_id, lb.event_team_id
FROM lab_bindings lb
WHERE lb.lab_group_name = sqlc.arg(lab_group_name)
ORDER BY lb.event_id, lb.event_team_id;

-- name: GetLabMonitoringCurrent :one
SELECT *
FROM lab_monitoring_current
WHERE event_id = sqlc.arg(event_id)
  AND event_team_id = sqlc.arg(event_team_id)
  AND lab_group_name = sqlc.arg(lab_group_name);

-- name: UpsertLabMonitoringCurrent :exec
INSERT INTO lab_monitoring_current (event_id, event_team_id, lab_group_name, agent_id, sequence, observed_at, updated_at, payload)
VALUES (sqlc.arg(event_id), sqlc.arg(event_team_id), sqlc.arg(lab_group_name), sqlc.arg(agent_id), sqlc.arg(sequence),
        sqlc.arg(observed_at), sqlc.arg(updated_at), sqlc.arg(payload))
ON CONFLICT (event_id, event_team_id, lab_group_name) DO UPDATE
SET agent_id = EXCLUDED.agent_id,
    sequence = EXCLUDED.sequence,
    observed_at = EXCLUDED.observed_at,
    updated_at = EXCLUDED.updated_at,
    payload = EXCLUDED.payload;

-- name: DeleteStaleLabMonitoringCurrent :exec
-- A snapshot is the agent's full picture: groups it no longer reports are gone.
DELETE FROM lab_monitoring_current
WHERE agent_id = sqlc.arg(agent_id)
  AND updated_at < sqlc.arg(before);

-- name: ListPlatformLabMonitoringCurrent :many
-- Active events by default; include_recent also lists everything the runner
-- touched since recent_since, whatever the event state.
SELECT c.event_id, e.name AS event_name, c.event_team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       t.moderators, c.lab_group_name, c.agent_id, c.sequence, c.observed_at, c.updated_at, c.payload
FROM lab_monitoring_current c
JOIN events e ON e.id = c.event_id
JOIN event_teams t ON t.id = c.event_team_id
WHERE (sqlc.arg(include_recent)::boolean AND c.updated_at >= sqlc.arg(recent_since)::timestamptz)
   OR (e.lifecycle_configured
       AND e.available_from <= sqlc.arg(now)::timestamptz
       AND (e.archive_at IS NULL OR e.archive_at > sqlc.arg(now)::timestamptz)
       AND e.start_at <= sqlc.arg(now)::timestamptz
       AND (e.finish_at IS NULL OR e.finish_at > sqlc.arg(now)::timestamptz)
       AND (e.manual_finished_at IS NULL OR e.manual_finished_at > sqlc.arg(now)::timestamptz)
       AND (e.withdraw_at IS NULL OR e.withdraw_at > sqlc.arg(now)::timestamptz))
ORDER BY e.name, team_name, c.lab_group_name;

-- name: ListEventLabMonitoringCurrent :many
-- The merged current state of every lab group of one event, for the organizers' stand list.
SELECT event_team_id, lab_group_name, payload
FROM lab_monitoring_current
WHERE event_id = sqlc.arg(event_id)
ORDER BY event_team_id, lab_group_name;

-- name: FindEventTeamForLabMonitoring :one
-- A team's group exists before question/Lab bindings, including the hidden
-- moderators group. The trusted group name is parsed by the repository.
SELECT event_id,id AS event_team_id FROM event_teams
WHERE event_id=sqlc.arg(event_id) AND id=sqlc.arg(event_team_id);
