-- name: UpsertUserVPNConfig :one
-- Store (or replace) a user's VPN config for a scope. The unique
-- (user_id, scope, scope_ref) constraint (NULLS NOT DISTINCT) makes this an
-- upsert: re-issuing within the same scope overwrites the ciphertext. Timestamps
-- come from the domain factory, not DB defaults.
INSERT INTO user_vpn_configs (id, user_id, scope, scope_ref, config, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (user_id, scope, scope_ref)
    DO UPDATE SET config     = excluded.config,
                  updated_at = excluded.updated_at
RETURNING *;

-- name: GetUserVPNConfig :one
-- IS NOT DISTINCT FROM matches a null scope_ref (the test scope) NULL-safely.
SELECT *
FROM user_vpn_configs
WHERE user_id = $1
  AND scope = $2
  AND scope_ref IS NOT DISTINCT FROM sqlc.narg(scope_ref);

-- name: ListUserVPNConfigs :many
SELECT *
FROM user_vpn_configs
WHERE user_id = $1
ORDER BY created_at;

-- name: DeleteUserVPNConfig :execrows
DELETE
FROM user_vpn_configs
WHERE user_id = $1
  AND scope = $2
  AND scope_ref IS NOT DISTINCT FROM sqlc.narg(scope_ref);
