-- name: FindActiveEventTeamsByLabGroup :many
SELECT DISTINCT lb.event_id, lb.event_team_id
FROM lab_bindings lb
JOIN events e ON e.id = lb.event_id
WHERE lb.lab_group_name = sqlc.arg(lab_group_name)
  AND e.lifecycle_configured
  AND e.available_from <= sqlc.arg(observed_at)
  AND (e.archive_at IS NULL OR e.archive_at > sqlc.arg(observed_at))
  AND e.start_at <= sqlc.arg(observed_at)
  AND (e.finish_at IS NULL OR e.finish_at > sqlc.arg(observed_at))
  AND (e.manual_finished_at IS NULL OR e.manual_finished_at > sqlc.arg(observed_at))
  AND (e.withdraw_at IS NULL OR e.withdraw_at > sqlc.arg(observed_at))
ORDER BY lb.event_id, lb.event_team_id;

-- name: CreateEventLabObservation :one
INSERT INTO event_lab_observations (
    id, event_id, event_team_id, lab_group_name, agent_id, sequence,
    observed_at, received_at, schema_version, snapshot, payload
)
VALUES (
    sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(event_team_id), sqlc.arg(lab_group_name), sqlc.arg(agent_id), sqlc.arg(sequence),
    sqlc.arg(observed_at), sqlc.arg(received_at), sqlc.arg(schema_version), sqlc.arg(snapshot), sqlc.arg(payload)
)
ON CONFLICT (agent_id, sequence, lab_group_name) DO NOTHING
RETURNING *;

-- name: GetEventLabObservationByAgentSequence :one
SELECT *
FROM event_lab_observations
WHERE agent_id = sqlc.arg(agent_id)
  AND sequence = sqlc.arg(sequence)
  AND lab_group_name = sqlc.arg(lab_group_name);

-- name: GetEventLabObservationCursor :one
SELECT observed_at, id
FROM event_lab_observations
WHERE event_id = sqlc.arg(event_id)
  AND id = sqlc.arg(id);

-- name: ListEventLabObservations :many
SELECT *
FROM event_lab_observations
WHERE event_id = sqlc.arg(event_id)
  AND (sqlc.narg(event_team_id)::uuid IS NULL OR event_team_id = sqlc.narg(event_team_id)::uuid)
  AND observed_at >= sqlc.arg(from_at)
  AND observed_at <= sqlc.arg(to_at)
  AND (observed_at, id) < (sqlc.arg(cursor_at), sqlc.arg(cursor_id)::uuid)
ORDER BY observed_at DESC, id DESC
LIMIT sqlc.arg(limit_val);

-- name: ListLatestEventLabObservations :many
SELECT DISTINCT ON (event_team_id, lab_group_name) *
FROM event_lab_observations
WHERE event_id = sqlc.arg(event_id)
ORDER BY event_team_id, lab_group_name, observed_at DESC, id DESC;

-- name: GetPlatformLabObservationCursor :one
SELECT observed_at, id
FROM event_lab_observations
WHERE id = sqlc.arg(id);

-- name: ListPlatformLabObservations :many
SELECT o.id, o.event_id, o.event_team_id, o.lab_group_name, o.agent_id, o.sequence,
       o.observed_at, o.received_at, o.schema_version, o.snapshot, o.payload,
       e.name AS event_name,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       t.moderators
FROM event_lab_observations o
JOIN events e ON e.id = o.event_id
JOIN event_teams t ON t.id = o.event_team_id
WHERE (sqlc.narg(event_id)::uuid IS NULL OR o.event_id = sqlc.narg(event_id)::uuid)
  AND (sqlc.narg(event_team_id)::uuid IS NULL OR o.event_team_id = sqlc.narg(event_team_id)::uuid)
  AND o.observed_at >= sqlc.arg(from_at)
  AND o.observed_at <= sqlc.arg(to_at)
  AND (o.observed_at, o.id) < (sqlc.arg(cursor_at), sqlc.arg(cursor_id)::uuid)
ORDER BY o.observed_at DESC, o.id DESC
LIMIT sqlc.arg(limit_val);
