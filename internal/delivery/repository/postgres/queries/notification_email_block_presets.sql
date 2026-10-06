-- name: ListEmailBlockPresets :many
SELECT *
FROM notification_email_block_presets
ORDER BY name;

-- name: GetEmailBlockPreset :one
SELECT *
FROM notification_email_block_presets
WHERE id = $1;

-- name: CreateEmailBlockPreset :one
INSERT INTO notification_email_block_presets (id, name, description, blocks)
VALUES ($1, $2, $3, $4) RETURNING *;

-- name: UpdateEmailBlockPreset :one
UPDATE notification_email_block_presets
SET name        = $2,
    description = $3,
    blocks      = $4,
    updated_at  = now()
WHERE id = $1 RETURNING *;

-- name: DeleteEmailBlockPreset :exec
DELETE
FROM notification_email_block_presets
WHERE id = $1;
