-- name: CreateUserPasskey :one
-- Refuses (no row) when the user already holds max_per_user passkeys: the cap and the insert are one statement.
INSERT INTO user_passkeys (id, user_id, credential_id, public_key, sign_count, aaguid, transports, user_verified,
                           backup_eligible, backup_state, name, created_at)
SELECT sqlc.arg(id), sqlc.arg(user_id), sqlc.arg(credential_id), sqlc.arg(public_key), sqlc.arg(sign_count),
       sqlc.arg(aaguid), sqlc.arg(transports)::text[], sqlc.arg(user_verified), sqlc.arg(backup_eligible),
       sqlc.arg(backup_state), sqlc.arg(name), sqlc.arg(created_at)
WHERE (SELECT count(*) FROM user_passkeys WHERE user_id = sqlc.arg(user_id)) < sqlc.arg(max_per_user)::bigint
RETURNING *;

-- name: GetUserPasskeyByCredentialID :one
SELECT *
FROM user_passkeys
WHERE credential_id = $1;

-- name: ListUserPasskeys :many
SELECT *
FROM user_passkeys
WHERE user_id = $1
ORDER BY created_at, id;

-- name: CountUserPasskeys :one
SELECT count(*)
FROM user_passkeys
WHERE user_id = $1;

-- name: UseUserPasskey :execrows
-- Records a sign-in. The counter must move forward (or stay at zero for authenticators that have none): a
-- replayed or cloned assertion matches no row, so two parallel uses of one counter value cannot both win.
UPDATE user_passkeys
SET sign_count   = sqlc.arg(sign_count),
    backup_state = sqlc.arg(backup_state),
    last_used_at = sqlc.arg(used_at)
WHERE id = sqlc.arg(id)
  AND compromised_at IS NULL
  AND (sign_count < sqlc.arg(sign_count) OR (sign_count = 0 AND sqlc.arg(sign_count) = 0));

-- name: MarkUserPasskeyCompromised :execrows
UPDATE user_passkeys
SET compromised_at = COALESCE(compromised_at, sqlc.arg(at))
WHERE id = sqlc.arg(id);

-- name: DeleteUserPasskey :execrows
DELETE
FROM user_passkeys
WHERE id = $1
  AND user_id = $2;

-- name: DeleteUserPasskeys :execrows
DELETE
FROM user_passkeys
WHERE user_id = $1;
