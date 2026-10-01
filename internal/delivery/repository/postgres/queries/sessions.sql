-- name: CreateSession :one
-- All timestamps come from the domain factory (NewSession), not DB defaults —
-- one source of truth for entity defaults.
INSERT INTO sessions (id, user_id, expires_at, last_seen, created_at, metadata)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: GetSessionByID :one
SELECT *
FROM sessions
WHERE id = $1;

-- name: TouchSession :execrows
UPDATE sessions
SET last_seen  = now(),
    expires_at = $2
WHERE id = $1;

-- name: GetSessionsByUser :many
SELECT *
FROM sessions
WHERE user_id = $1
ORDER BY last_seen DESC;

-- name: DeleteSession :execrows
DELETE
FROM sessions
WHERE id = $1;

-- name: DeleteUserSession :execrows
DELETE
FROM sessions
WHERE id = $1
  AND user_id = $2;

-- name: DeleteUserSessionsExcept :execrows
DELETE
FROM sessions
WHERE user_id = $1
  AND id <> $2;

-- name: DeleteUserSessions :execrows
DELETE
FROM sessions
WHERE user_id = $1;
