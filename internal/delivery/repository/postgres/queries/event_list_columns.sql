-- name: GetEventListColumns :one
SELECT *
FROM event_list_columns
WHERE event_id = sqlc.arg(event_id)
  AND list = sqlc.arg(list);

-- name: UpsertEventListColumns :one
INSERT INTO event_list_columns (event_id, list, columns, updated_at, updated_by)
VALUES (sqlc.arg(event_id), sqlc.arg(list), sqlc.arg(columns), sqlc.arg(updated_at), sqlc.arg(updated_by))
ON CONFLICT (event_id, list) DO UPDATE
    SET columns    = EXCLUDED.columns,
        updated_at = EXCLUDED.updated_at,
        updated_by = EXCLUDED.updated_by
RETURNING *;
