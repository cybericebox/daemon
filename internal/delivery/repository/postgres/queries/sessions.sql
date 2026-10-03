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
-- last_seen is written at most once per 30 s window per session by the replica that serves it; GREATEST keeps a
-- late write from moving it back. The idle deadline follows it.
UPDATE sessions
SET last_seen  = GREATEST(last_seen, sqlc.arg(seen_at)),
    expires_at = GREATEST(expires_at, sqlc.arg(expires_at))
WHERE id = sqlc.arg(id);

-- name: GetSessionsByUser :many
SELECT *
FROM sessions
WHERE user_id = $1
ORDER BY last_seen DESC;

-- Ending a session deletes its row and writes the revocation row in ONE statement (a session never ends
-- without its revocation). expires_at is when the cookie would die by itself: the smaller of sign-in + absolute TTL
-- and last_seen + idle TTL + 1 min margin.

-- name: RevokeSession :many
WITH gone AS (DELETE FROM sessions WHERE id = sqlc.arg(id) RETURNING id, user_id, created_at, last_seen)
INSERT
INTO session_revocations (session_id, user_id, expires_at)
SELECT id,
       user_id,
       LEAST(created_at + make_interval(secs => sqlc.arg(absolute_secs)::float8),
             last_seen + make_interval(secs => sqlc.arg(idle_secs)::float8) + interval '1 minute')
FROM gone
RETURNING session_id, expires_at;

-- name: RevokeUserSession :many
WITH gone AS (DELETE FROM sessions WHERE id = sqlc.arg(id) AND sessions.user_id = sqlc.arg(owner_id) RETURNING id, user_id, created_at, last_seen)
INSERT
INTO session_revocations (session_id, user_id, expires_at)
SELECT id,
       user_id,
       LEAST(created_at + make_interval(secs => sqlc.arg(absolute_secs)::float8),
             last_seen + make_interval(secs => sqlc.arg(idle_secs)::float8) + interval '1 minute')
FROM gone
RETURNING session_id, expires_at;

-- name: RevokeUserSessionsExcept :many
WITH gone AS (DELETE FROM sessions WHERE sessions.user_id = sqlc.arg(owner_id) AND id <> sqlc.arg(except_id) RETURNING id, user_id, created_at, last_seen)
INSERT
INTO session_revocations (session_id, user_id, expires_at)
SELECT id,
       user_id,
       LEAST(created_at + make_interval(secs => sqlc.arg(absolute_secs)::float8),
             last_seen + make_interval(secs => sqlc.arg(idle_secs)::float8) + interval '1 minute')
FROM gone
RETURNING session_id, expires_at;

-- name: RevokeUserSessions :many
WITH gone AS (DELETE FROM sessions WHERE sessions.user_id = sqlc.arg(owner_id) RETURNING id, user_id, created_at, last_seen)
INSERT
INTO session_revocations (session_id, user_id, expires_at)
SELECT id,
       user_id,
       LEAST(created_at + make_interval(secs => sqlc.arg(absolute_secs)::float8),
             last_seen + make_interval(secs => sqlc.arg(idle_secs)::float8) + interval '1 minute')
FROM gone
RETURNING session_id, expires_at;

-- name: GetDatabaseTime :one
SELECT now()::timestamptz AS db_time;

-- name: ListActiveSessionRevocations :many
-- What a replica loads before it serves: the revocations whose cookies could still be alive.
SELECT seq, session_id, user_id, revoked_at, expires_at
FROM session_revocations
WHERE expires_at > now()
ORDER BY seq;

-- name: ListSessionRevocationsSince :many
-- The poll: rows revoked after the watermark (the caller passes watermark - the 10 s overlap).
SELECT seq, session_id, user_id, revoked_at, expires_at
FROM session_revocations
WHERE revoked_at > $1
ORDER BY seq;

-- name: DeleteExpiredSessionRevocations :execrows
DELETE
FROM session_revocations
WHERE expires_at < now();
