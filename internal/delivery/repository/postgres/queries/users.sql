-- name: GetUserByEmail :one
-- The address arrives normalized (trimmed, lower case): the plain unique
-- index on email does the lookup.
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
SET last_seen = GREATEST(last_seen, sqlc.arg(seen_at))
WHERE id = sqlc.arg(id);

-- name: GetUserAccess :one
-- The one query per signed-in request: the global role and the status (blocked) of the caller.
SELECT role, status
FROM users
WHERE id = $1
  AND deleted_at IS NULL;

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
    + (SELECT count(*) FROM user_providers WHERE user_id = users.id)
    + (SELECT count(*) FROM user_passkeys WHERE user_id = users.id)) ::bigint
FROM users
WHERE users.id = $1;

-- name: ListUsersCursor :many
SELECT *
FROM users
WHERE deleted_at IS NULL
  AND (sqlc.arg(search)::text = '' OR email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR first_name ILIKE '%' || sqlc.arg(search)::text || '%'
       OR last_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (coalesce(cardinality(sqlc.arg(roles)::text[]), 0) = 0 OR role = ANY (sqlc.arg(roles)::text[]))
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
  AND (coalesce(cardinality(sqlc.arg(roles)::text[]), 0) = 0 OR role = ANY (sqlc.arg(roles)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(email_confirmed)::text = '' OR email_confirmed = (sqlc.arg(email_confirmed)::text = 'true'))
  AND (sqlc.arg(sign_in)::text = ''
       OR (sqlc.arg(sign_in)::text = 'password' AND hashed_password IS NOT NULL AND hashed_password <> '')
       OR (sqlc.arg(sign_in)::text = 'google' AND EXISTS (SELECT 1 FROM user_providers up WHERE up.user_id = users.id AND up.provider = 'google')))
  AND (sqlc.arg(inactive_days)::int = 0 OR last_seen < now() - make_interval(days => sqlc.arg(inactive_days)::int));

-- name: ListUsersPage :many
-- Offset mode is used by the admin table: column sorting and a page count
-- cannot be expressed by the legacy created_at cursor.
SELECT *
FROM users
WHERE deleted_at IS NULL
  AND (sqlc.arg(search)::text = '' OR email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR first_name ILIKE '%' || sqlc.arg(search)::text || '%'
       OR last_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (coalesce(cardinality(sqlc.arg(roles)::text[]), 0) = 0 OR role = ANY (sqlc.arg(roles)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(email_confirmed)::text = '' OR email_confirmed = (sqlc.arg(email_confirmed)::text = 'true'))
  AND (sqlc.arg(sign_in)::text = ''
       OR (sqlc.arg(sign_in)::text = 'password' AND hashed_password IS NOT NULL AND hashed_password <> '')
       OR (sqlc.arg(sign_in)::text = 'google' AND EXISTS (SELECT 1 FROM user_providers up WHERE up.user_id = users.id AND up.provider = 'google')))
  AND (sqlc.arg(inactive_days)::int = 0 OR last_seen < now() - make_interval(days => sqlc.arg(inactive_days)::int))
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
  CASE WHEN sqlc.arg(sort_by)::text = 'email' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(email) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'email' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(email) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'signIn' AND sqlc.arg(sort_dir)::text = 'asc' THEN (CASE WHEN hashed_password IS NOT NULL AND hashed_password <> '' THEN 0 ELSE 2 END) + (CASE WHEN EXISTS (SELECT 1 FROM user_providers up WHERE up.user_id = users.id AND up.provider = 'google') THEN 0 ELSE 1 END) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'signIn' AND sqlc.arg(sort_dir)::text = 'desc' THEN (CASE WHEN hashed_password IS NOT NULL AND hashed_password <> '' THEN 0 ELSE 2 END) + (CASE WHEN EXISTS (SELECT 1 FROM user_providers up WHERE up.user_id = users.id AND up.provider = 'google') THEN 0 ELSE 1 END) END DESC,
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

-- name: ListUserParticipations :many
-- Where the person takes part (a participant, with the team and the role in it) or works (an event manager), newest
-- event first. One query for the admin user card.
SELECT e.id                                     AS event_id,
       e.name                                   AS event_name,
       e.tag                                    AS event_tag,
       p.team_id,
       COALESCE(t.name, '')::text               AS team_name,
       CASE WHEN p.team_id IS NULL THEN 'participant'
            WHEN p.team_role = 0 THEN 'captain'
            ELSE 'member' END::text             AS role,
       p.status::smallint                       AS status,
       e.start_at
FROM event_participants p
JOIN events e ON e.id = p.event_id
LEFT JOIN event_teams t ON t.id = p.team_id
WHERE p.user_id = sqlc.arg(user_id)
UNION ALL
SELECT e.id, e.name, e.tag, NULL::uuid, ''::text,
       CASE m.role WHEN 0 THEN 'owner' WHEN 1 THEN 'manager' ELSE 'viewer' END::text,
       2::smallint, e.start_at
FROM event_managers m
JOIN events e ON e.id = m.event_id
WHERE m.user_id = sqlc.arg(user_id)
ORDER BY start_at DESC, event_id
LIMIT 500;

-- name: CountUsersFacet :many
-- How many users have each value of a field under the other filters of the table (the caller leaves the faceted
-- field's own filter empty). field: role, status, emailConfirmed or signIn (a user counts once per method).
SELECT v.value::text AS value, count(*)::bigint AS total
FROM users
CROSS JOIN LATERAL (
    SELECT unnest(CASE sqlc.arg(field)::text
        WHEN 'role' THEN ARRAY[users.role]
        WHEN 'status' THEN ARRAY[users.status]
        WHEN 'emailConfirmed' THEN ARRAY[users.email_confirmed::text]
        WHEN 'signIn' THEN array_remove(ARRAY[
            CASE WHEN users.hashed_password IS NOT NULL AND users.hashed_password <> '' THEN 'password' END,
            CASE WHEN EXISTS (SELECT 1 FROM user_providers up WHERE up.user_id = users.id AND up.provider = 'google') THEN 'google' END], NULL)
        ELSE ARRAY[]::text[] END) AS value) v
WHERE deleted_at IS NULL
  AND (sqlc.arg(search)::text = '' OR email ILIKE '%' || sqlc.arg(search)::text || '%'
       OR first_name ILIKE '%' || sqlc.arg(search)::text || '%'
       OR last_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (coalesce(cardinality(sqlc.arg(roles)::text[]), 0) = 0 OR role = ANY (sqlc.arg(roles)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.arg(email_confirmed)::text = '' OR email_confirmed = (sqlc.arg(email_confirmed)::text = 'true'))
  AND (sqlc.arg(sign_in)::text = ''
       OR (sqlc.arg(sign_in)::text = 'password' AND hashed_password IS NOT NULL AND hashed_password <> '')
       OR (sqlc.arg(sign_in)::text = 'google' AND EXISTS (SELECT 1 FROM user_providers up WHERE up.user_id = users.id AND up.provider = 'google')))
  AND (sqlc.arg(inactive_days)::int = 0 OR last_seen < now() - make_interval(days => sqlc.arg(inactive_days)::int))
GROUP BY v.value
ORDER BY v.value;
