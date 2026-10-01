-- name: CreateAdminAuditLog :exec
INSERT INTO admin_audit_log (id, actor_id, permission, method, route, response_status, created_at, target)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: ListAdminAuditLog :many
SELECT id, actor_id, permission, method, route, response_status, created_at, target
FROM admin_audit_log
WHERE (sqlc.narg(actor_id)::uuid IS NULL OR actor_id = sqlc.narg(actor_id)::uuid)
  AND (sqlc.narg(permission)::text IS NULL OR permission = sqlc.narg(permission)::text)
  AND (sqlc.narg(route)::text IS NULL OR route = sqlc.narg(route)::text)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_val);
