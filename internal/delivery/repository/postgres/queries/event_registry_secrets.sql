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

-- name: UpdateEventRegistrySecretCheck :execrows
-- The periodic check result, written apart from the editable entity (the narrow-query exception, like the
-- batched last_seen): only while the secret is still the one that was checked (updated_at), so a result for a
-- token that was replaced meanwhile is dropped.
UPDATE event_registry_secrets
SET credential_check = $3
WHERE id = $1
  AND updated_at = $2;

-- name: ListEventRegistrySecretsToCheck :many
-- The secrets the periodic check covers: those of events that are not finished and not archived.
SELECT s.id, s.event_id, s.host, s.repository_prefix, s.username, s.token_ciphertext, s.created_at, s.updated_at,
       s.credential_check, e.tag AS event_tag, e.name AS event_name
FROM event_registry_secrets s
         JOIN events e ON e.id = s.event_id
WHERE (e.finish_at IS NULL OR e.finish_at > sqlc.arg(now)::timestamptz)
  AND (e.archive_at IS NULL OR e.archive_at > sqlc.arg(now)::timestamptz)
ORDER BY s.event_id, s.host, s.repository_prefix;
