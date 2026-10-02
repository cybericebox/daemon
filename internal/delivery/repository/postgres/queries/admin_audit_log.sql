-- name: CreateAdminAuditLog :exec
INSERT INTO admin_audit_log (id, actor_id, permission, method, route, response_status, created_at, target)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAdminAuditLog :many
-- One page of the journal, newest first, keyset-paged by (created_at, id).
-- Every filter is optional. route and target_id are "contains" matches
-- (strpos, so no LIKE escaping); target_kind matches the "kind:" token of the
-- space-separated target ("event:<id> team:<id>"); a status class is
-- status_min..status_max; cursor_created_at/cursor_id continue after the
-- last row of the previous page.
SELECT id, actor_id, permission, method, route, response_status, created_at, target
FROM admin_audit_log
WHERE (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(permission)::text IS NULL OR permission = sqlc.narg(permission)::text)
  AND (sqlc.narg(route)::text IS NULL OR strpos(route, sqlc.narg(route)::text) > 0)
  AND (sqlc.narg(method)::text IS NULL OR method = sqlc.narg(method)::text)
  AND (sqlc.narg(status_min)::int IS NULL OR response_status >= sqlc.narg(status_min)::int)
  AND (sqlc.narg(status_max)::int IS NULL OR response_status <= sqlc.narg(status_max)::int)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR created_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR created_at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.narg(target_kind)::text IS NULL
       OR strpos(' ' || target, ' ' || sqlc.narg(target_kind)::text || ':') > 0)
  AND (sqlc.narg(target_id)::text IS NULL OR strpos(target, sqlc.narg(target_id)::text) > 0)
  AND (sqlc.narg(cursor_created_at)::timestamptz IS NULL
       OR (created_at, id) < (sqlc.narg(cursor_created_at)::timestamptz, sqlc.narg(cursor_id)::uuid))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_val);
