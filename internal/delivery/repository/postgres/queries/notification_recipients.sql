-- name: ListEventWriteManagerUserIDs :many
-- Owners and moderators: the memberships that may act on participants
-- (viewers are read-only).
SELECT user_id
FROM event_managers
WHERE event_id = sqlc.arg(event_id)
  AND role IN (0, 1)
ORDER BY user_id;

-- name: ListEventManagerUserIDs :many
-- Every management membership of the Event (owner, moderator, viewer).
SELECT user_id
FROM event_managers
WHERE event_id = sqlc.arg(event_id)
ORDER BY user_id;

-- name: ListPlatformAdminUserIDs :many
-- Active platform administrators (super admins and admins; read-only admin
-- viewers cannot act on requests).
SELECT id
FROM users
WHERE role IN ('super_admin', 'admin')
  AND status = 'active'
  AND deleted_at IS NULL
ORDER BY id;
