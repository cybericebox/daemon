-- name: ListEventRegistrySecrets :many
SELECT *
FROM event_registry_secrets
WHERE event_id = $1
ORDER BY host, repository_prefix;

-- name: GetEventRegistrySecret :one
-- Scoped by event: a secret of another event is simply not found.
SELECT *
FROM event_registry_secrets
WHERE id = $1
  AND event_id = $2;

-- name: InsertEventRegistrySecret :exec
INSERT INTO event_registry_secrets (id, event_id, host, repository_prefix, username, token_ciphertext, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8);

-- name: UpdateEventRegistrySecret :execrows
-- The whole editable entity in one statement; id and event_id never change.
UPDATE event_registry_secrets
SET host              = $3,
    repository_prefix = $4,
    username          = $5,
    token_ciphertext  = $6,
    updated_at        = $7
WHERE id = $1
  AND event_id = $2;

-- name: DeleteEventRegistrySecret :execrows
DELETE
FROM event_registry_secrets
WHERE id = $1
  AND event_id = $2;
