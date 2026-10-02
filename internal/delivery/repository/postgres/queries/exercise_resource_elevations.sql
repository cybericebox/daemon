-- name: CreateExerciseResourceElevation :exec
-- Timestamps and the id come from the domain factory. The partial unique index allows one pending request per
-- exercise.
INSERT INTO exercise_resource_elevations (id, exercise_id, version_id, status, reason, requested, requested_by, requested_at)
VALUES (sqlc.arg(id), sqlc.arg(exercise_id), sqlc.narg(version_id), 0, sqlc.arg(reason), sqlc.arg(requested), sqlc.narg(requested_by), sqlc.arg(requested_at));

-- name: GetExerciseResourceElevation :one
SELECT *
FROM exercise_resource_elevations
WHERE id = $1;

-- name: DecideExerciseResourceElevation :execrows
-- Only a pending request can be decided; 0 rows: it was decided meanwhile.
UPDATE exercise_resource_elevations
SET status        = sqlc.arg(status),
    approved      = sqlc.narg(approved),
    decision_note = sqlc.arg(decision_note),
    decided_by    = sqlc.narg(decided_by),
    decided_at    = sqlc.arg(decided_at)
WHERE id = sqlc.arg(id)
  AND status = 0;

-- name: ListExerciseResourceElevations :many
-- Newest first; status and exercise narrow the list (a null argument does not filter).
SELECT elevation.*, exercise.name AS exercise_name
FROM exercise_resource_elevations elevation
         JOIN exercises exercise ON exercise.id = elevation.exercise_id
WHERE (sqlc.narg(status)::smallint IS NULL OR elevation.status = sqlc.narg(status)::smallint)
  AND (sqlc.narg(exercise_id)::uuid IS NULL OR elevation.exercise_id = sqlc.narg(exercise_id)::uuid)
ORDER BY elevation.requested_at DESC, elevation.id DESC;

-- name: GetLatestExerciseResourceElevation :one
-- The open request of the exercise, else its latest decided one.
SELECT *
FROM exercise_resource_elevations
WHERE exercise_id = $1
ORDER BY (status = 0) DESC, requested_at DESC, id DESC
LIMIT 1;

-- name: ListApprovedExerciseResourceElevations :many
-- The approved values of every approved elevation of the given exercises.
SELECT exercise_id, approved
FROM exercise_resource_elevations
WHERE exercise_id = ANY (sqlc.arg(exercise_ids)::uuid[])
  AND status = 1
  AND approved IS NOT NULL;
