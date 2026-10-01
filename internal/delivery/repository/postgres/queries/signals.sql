-- name: CreateSignalOutbox :one
INSERT INTO signal_outbox (id, signal_type, occurred_at, payload, available_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(signal_type), sqlc.arg(occurred_at), sqlc.arg(payload), sqlc.arg(available_at), sqlc.arg(created_at))
RETURNING *;

-- name: ClaimSignalOutboxBatch :many
WITH candidates AS (
    SELECT id
    FROM signal_outbox
    WHERE status = 'pending'
      AND available_at <= sqlc.arg(now_at)
    ORDER BY occurred_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT sqlc.arg(batch_size)
)
UPDATE signal_outbox AS outbox
SET status = 'processing',
    attempts = outbox.attempts + 1,
    claimed_at = sqlc.arg(now_at)
FROM candidates
WHERE outbox.id = candidates.id
RETURNING outbox.*;

-- name: CompleteSignalOutbox :execrows
UPDATE signal_outbox
SET status = 'completed', completed_at = sqlc.arg(completed_at), last_error = ''
WHERE id = sqlc.arg(id) AND status = 'processing';

-- name: RetrySignalOutbox :execrows
UPDATE signal_outbox
SET status = 'pending', available_at = sqlc.arg(available_at), last_error = sqlc.arg(last_error)
WHERE id = sqlc.arg(id) AND status = 'processing';

-- name: EnsureSignalHookExecution :exec
INSERT INTO signal_hook_executions (signal_id, hook_name, available_at, created_at)
VALUES (sqlc.arg(signal_id), sqlc.arg(hook_name), sqlc.arg(available_at), sqlc.arg(created_at))
ON CONFLICT (signal_id, hook_name) DO NOTHING;

-- name: ClaimSignalHookExecution :one
UPDATE signal_hook_executions
SET status = 'processing', attempts = attempts + 1, claimed_at = sqlc.arg(now_at)
WHERE signal_id = sqlc.arg(signal_id)
  AND hook_name = sqlc.arg(hook_name)
  AND status = 'pending'
  AND available_at <= sqlc.arg(now_at)
RETURNING *;

-- name: CompleteSignalHookExecution :execrows
UPDATE signal_hook_executions
SET status = 'completed', completed_at = sqlc.arg(completed_at), last_error = ''
WHERE signal_id = sqlc.arg(signal_id)
  AND hook_name = sqlc.arg(hook_name)
  AND status = 'processing';

-- name: RetrySignalHookExecution :execrows
UPDATE signal_hook_executions
SET status = 'pending', available_at = sqlc.arg(available_at), last_error = sqlc.arg(last_error)
WHERE signal_id = sqlc.arg(signal_id)
  AND hook_name = sqlc.arg(hook_name)
  AND status = 'processing';

-- name: CountIncompleteSignalHookExecutions :one
SELECT count(*)
FROM signal_hook_executions
WHERE signal_id = sqlc.arg(signal_id)
  AND status != 'completed';

-- name: CreateSecretEnvelope :one
INSERT INTO secret_envelopes (
    id, purpose, key_version, signal_type, field_path, scope_event_id,
    recipient_user_id, ciphertext, wrapped_data_key, expires_at, created_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(purpose), sqlc.arg(key_version), sqlc.arg(signal_type), sqlc.arg(field_path), sqlc.arg(scope_event_id),
    sqlc.arg(recipient_user_id), sqlc.arg(ciphertext), sqlc.arg(wrapped_data_key), sqlc.arg(expires_at), sqlc.arg(created_at)
)
RETURNING *;

-- name: GetSecretEnvelope :one
SELECT *
FROM secret_envelopes
WHERE id = sqlc.arg(id);
