-- name: GetUserByEmail :one
SELECT *
FROM users
WHERE email = $1
  AND deleted_at IS NULL;

-- name: GetUserByID :one
SELECT *
FROM users
WHERE id = $1
  AND deleted_at IS NULL;

-- name: CreateUser :one
-- Timestamps come from the domain factory, not DB defaults — one source of
-- truth for entity defaults.
INSERT INTO users (id, email, first_name, last_name, hashed_password, picture, role, status,
                   email_confirmed, last_seen, updated_at, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12) RETURNING *;

-- name: UpdateUser :execrows
-- Whole-aggregate write for the User entity: the repository maps the full
-- domain model here. Deliberately excludes last_seen (owned by the async
-- touch hot path — including it would race and lose updates) and created_at
-- (immutable). updated_at comes from the domain (touch), not now().
UPDATE users
SET email           = $2,
    first_name      = $3,
    last_name       = $4,
    hashed_password = $5,
    picture         = $6,
    role            = $7,
    status          = $8,
    email_confirmed = $9,
    tos_accepted_at = $10,
    tos_version     = $11,
    updated_at      = $12,
    updated_by      = $13,
    deleted_at      = $14
WHERE id = $1
  -- Optimistic lock: the caller sends the updated_at it loaded; a concurrent
  -- write moved it, so 0 rows means "modified since read" (or gone).
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: UpdateUserLastSeen :execrows
UPDATE users
SET last_seen = now()
WHERE id = $1;

-- name: MarkUserInvitationSent :execrows
-- Narrow write outside the aggregate UPDATE set (like last_seen): the last
-- time an invitation email was queued for a still unconfirmed account; the
-- retention clock of unconfirmed accounts runs from it.
UPDATE users
SET invitation_sent_at = sqlc.arg(sent_at)::timestamptz
WHERE id = sqlc.arg(id)
  AND status = 'incomplete';

-- name: CountUserLoginMethods :one
SELECT ((CASE WHEN hashed_password IS NOT NULL AND hashed_password <> '' THEN 1 ELSE 0 END)
    + (SELECT count(*) FROM user_providers WHERE user_id = users.id)) ::bigint
FROM users
WHERE users.id = $1;

-- name: ListUsersCursor :many
SELECT *
FROM users
WHERE deleted_at IS NULL
  AND (sqlc.arg(search)::text = '' OR email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR first_name ILIKE '%' || sqlc.arg(search)::text || '%'
       OR last_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (cardinality(sqlc.arg(roles)::text[]) = 0 OR role = ANY (sqlc.arg(roles)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC LIMIT sqlc.arg(limit_val);

-- name: CountUsers :one
SELECT count(*)
FROM users
WHERE deleted_at IS NULL
  AND (sqlc.arg(search)::text = '' OR email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR first_name ILIKE '%' || sqlc.arg(search)::text || '%'
       OR last_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (cardinality(sqlc.arg(roles)::text[]) = 0 OR role = ANY (sqlc.arg(roles)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text);

-- name: ListUsersPage :many
-- Offset mode is used by the admin table: column sorting and a page count
-- cannot be expressed by the legacy created_at cursor.
SELECT *
FROM users
WHERE deleted_at IS NULL
  AND (sqlc.arg(search)::text = '' OR email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR first_name ILIKE '%' || sqlc.arg(search)::text || '%'
       OR last_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (cardinality(sqlc.arg(roles)::text[]) = 0 OR role = ANY (sqlc.arg(roles)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'name' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(coalesce(nullif(btrim(first_name || ' ' || last_name), ''), email)) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'name' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(coalesce(nullif(btrim(first_name || ' ' || last_name), ''), email)) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'role' AND sqlc.arg(sort_dir)::text = 'asc' THEN role END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'role' AND sqlc.arg(sort_dir)::text = 'desc' THEN role END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'asc' THEN status END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'desc' THEN status END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'created' AND sqlc.arg(sort_dir)::text = 'asc' THEN created_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'created' AND sqlc.arg(sort_dir)::text = 'desc' THEN created_at END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'lastSeen' AND sqlc.arg(sort_dir)::text = 'asc' THEN last_seen END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'lastSeen' AND sqlc.arg(sort_dir)::text = 'desc' THEN last_seen END DESC,
  id DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountUsersByRole :one
SELECT count(*)
FROM users
WHERE role = $1
  AND deleted_at IS NULL;

-- name: CountUsersByRoleAll :many
SELECT role, count(*) ::bigint AS count
FROM users
WHERE deleted_at IS NULL
GROUP BY role;

-- name: CountUsersByStatus :one
SELECT count(*)
FROM users
WHERE deleted_at IS NULL
  AND status = $1;

-- name: CountUsersCreatedSince :one
SELECT count(*)
FROM users
WHERE deleted_at IS NULL
  AND created_at >= $1;

-- name: CountUsersActiveSince :one
SELECT count(*)
FROM users
WHERE deleted_at IS NULL
  AND last_seen >= $1;

-- name: AvgDailyActiveSince :one
SELECT (COALESCE(count(*), 0)::float8 / 7.0) ::float8 AS avg_daily_active
FROM (SELECT date_trunc('day', created_at) AS d, user_id
      FROM sessions
      WHERE created_at >= $1
      GROUP BY 1, 2) x;

-- name: RegistrationsByDaySince :many
SELECT date_trunc('day', created_at) ::timestamptz AS day, count(*)::bigint AS count
FROM users
WHERE deleted_at IS NULL
  AND created_at >= $1
GROUP BY 1
ORDER BY 1;
