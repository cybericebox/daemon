-- name: CreatePlatformLabCapacityObservation :one
INSERT INTO platform_lab_capacity_observations (
    id, agent_id, sequence, observed_at, received_at, schema_version, snapshot, payload
)
VALUES (
    sqlc.arg(id), sqlc.arg(agent_id), sqlc.arg(sequence), sqlc.arg(observed_at), sqlc.arg(received_at),
    sqlc.arg(schema_version), sqlc.arg(snapshot), sqlc.arg(payload)
)
ON CONFLICT (agent_id, sequence) DO NOTHING
RETURNING *;

-- name: GetPlatformLabCapacityObservationByAgentSequence :one
SELECT *
FROM platform_lab_capacity_observations
WHERE agent_id = sqlc.arg(agent_id)
  AND sequence = sqlc.arg(sequence);

-- name: ListLatestPlatformLabCapacityObservations :many
SELECT DISTINCT ON (agent_id) *
FROM platform_lab_capacity_observations
ORDER BY agent_id, observed_at DESC, id DESC;

-- name: GetPlatformLabCapacityObservationCursor :one
SELECT observed_at, id
FROM platform_lab_capacity_observations
WHERE id = sqlc.arg(id);

-- name: ListPlatformLabCapacityObservations :many
SELECT *
FROM platform_lab_capacity_observations
WHERE observed_at >= sqlc.arg(from_at)
  AND observed_at <= sqlc.arg(to_at)
  AND (observed_at, id) < (sqlc.arg(cursor_at), sqlc.arg(cursor_id)::uuid)
ORDER BY observed_at DESC, id DESC
LIMIT sqlc.arg(limit_val);
