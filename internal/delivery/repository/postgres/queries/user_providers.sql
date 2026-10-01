-- name: CreateUserProvider :one
INSERT INTO user_providers (id, user_id, provider, provider_user_id)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: GetUserByProvider :one
SELECT u.*
FROM users u
         JOIN user_providers p ON p.user_id = u.id
WHERE p.provider = $1
  AND p.provider_user_id = $2
  AND u.deleted_at IS NULL;

-- name: GetUserProviders :many
SELECT *
FROM user_providers
WHERE user_id = $1;

-- name: DeleteUserProvider :execrows
DELETE
FROM user_providers
WHERE user_id = $1
  AND provider = $2;

-- name: DeleteUserProviders :execrows
DELETE
FROM user_providers
WHERE user_id = $1;
