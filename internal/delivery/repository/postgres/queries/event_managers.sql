-- name: CreateEventManager :one
INSERT INTO event_managers (event_id, user_id, role, created_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetEventManager :one
SELECT *
FROM event_managers
WHERE event_id = $1
  AND user_id = $2;

-- name: ListEventManagers :many
SELECT *
FROM event_managers
WHERE event_id = $1
ORDER BY role, created_at, user_id;

-- name: UpsertEventManager :one
INSERT INTO event_managers (event_id, user_id, role, created_at)
VALUES (sqlc.arg(event_id), sqlc.arg(user_id), sqlc.arg(role), sqlc.arg(created_at))
ON CONFLICT (event_id, user_id) DO UPDATE
    SET role = EXCLUDED.role
    WHERE event_managers.role <> 0
RETURNING *;

-- name: DeleteNonOwnerEventManager :execrows
DELETE FROM event_managers
WHERE event_id = sqlc.arg(event_id)
  AND user_id = sqlc.arg(user_id)
  AND role <> 0;
