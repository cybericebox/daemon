-- name: CreateEventStage :one
INSERT INTO event_stages (id, event_id, name, opens_at, closes_at, returnable, lab_retention_minutes, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(name), sqlc.arg(opens_at), sqlc.arg(closes_at), sqlc.arg(returnable),sqlc.narg(lab_retention_minutes),
 sqlc.arg(created_at), sqlc.arg(updated_at))
RETURNING *;

-- name: GetEventStage :one
SELECT *
FROM event_stages
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: ListEventStages :many
-- Order is time: stages never overlap.
SELECT *
FROM event_stages
WHERE event_id = sqlc.arg(event_id)
ORDER BY opens_at ASC, id ASC;

-- name: UpdateEventStage :one
UPDATE event_stages
SET lab_retention_minutes=sqlc.narg(lab_retention_minutes),name       = sqlc.arg(name),
    opens_at   = sqlc.arg(opens_at),
    closes_at  = sqlc.arg(closes_at),
    returnable = sqlc.arg(returnable),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
RETURNING *;

-- name: DeleteEventStage :execrows
DELETE FROM event_stages
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: CountEventStageSets :one
-- Every set that still points at the stage (detached ones too: the foreign key would refuse the delete).
SELECT count(*)::integer
FROM event_exercises
WHERE stage_id = sqlc.arg(stage_id);

-- name: SetEventExerciseStage :one
UPDATE event_exercises
SET stage_id = sqlc.narg(stage_id)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND status = 0
RETURNING *;
