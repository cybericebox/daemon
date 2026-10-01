-- name: GetEventResultRevision :one
SELECT revision, updated_at
FROM event_result_revisions
WHERE event_id = sqlc.arg(event_id);

-- name: AdvanceEventResultRevision :one
INSERT INTO event_result_revisions (event_id, revision, updated_at)
VALUES (sqlc.arg(event_id), 1, sqlc.arg(updated_at))
ON CONFLICT (event_id) DO UPDATE
SET revision   = event_result_revisions.revision + 1,
    updated_at = EXCLUDED.updated_at
RETURNING revision, updated_at;

-- name: CreateEventResultChange :one
INSERT INTO event_result_changes (event_id, revision, kind, payload, created_at)
VALUES (sqlc.arg(event_id), sqlc.arg(revision), sqlc.arg(kind), sqlc.arg(payload), sqlc.arg(created_at))
RETURNING event_id, revision, kind, payload, created_at;

-- name: ListEventResultChangesAfter :many
SELECT event_id, revision, kind, payload, created_at
FROM event_result_changes
WHERE event_id = sqlc.arg(event_id)
  AND revision > sqlc.arg(after_revision)
ORDER BY revision ASC
LIMIT sqlc.arg(limit_val);

-- name: GetEarliestEventResultChangeRevision :one
SELECT COALESCE(MIN(revision), 0)::bigint AS revision
FROM event_result_changes
WHERE event_id = sqlc.arg(event_id);

-- name: DeleteEventResultChangesBefore :execrows
DELETE FROM event_result_changes
WHERE created_at < sqlc.arg(before);
