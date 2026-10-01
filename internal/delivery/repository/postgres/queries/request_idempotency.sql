-- name: ReserveRequestIdempotency :many
INSERT INTO request_idempotency (owner_id, scope, key, request_hash, created_at, expires_at)
VALUES (sqlc.arg(owner_id), sqlc.arg(scope), sqlc.arg(key), sqlc.arg(request_hash), sqlc.arg(created_at), sqlc.arg(expires_at))
ON CONFLICT (owner_id, scope, key) DO NOTHING
RETURNING owner_id, scope, key, request_hash, completed, response_status, response_body, created_at, expires_at;

-- name: GetRequestIdempotency :one
SELECT owner_id, scope, key, request_hash, completed, response_status, response_body, created_at, expires_at
FROM request_idempotency
WHERE owner_id = sqlc.arg(owner_id)
  AND scope = sqlc.arg(scope)
  AND key = sqlc.arg(key);

-- name: CompleteRequestIdempotency :execrows
UPDATE request_idempotency
SET completed = TRUE,
    response_status = sqlc.arg(response_status),
    response_body = sqlc.arg(response_body)
WHERE owner_id = sqlc.arg(owner_id)
  AND scope = sqlc.arg(scope)
  AND key = sqlc.arg(key)
  AND request_hash = sqlc.arg(request_hash)
  AND NOT completed;

-- name: DeleteExpiredRequestIdempotency :execrows
DELETE FROM request_idempotency
WHERE completed
  AND expires_at <= sqlc.arg(expires_at);
