-- name: UpsertPlatformSetting :one
-- Atomic get-or-create-or-update: key is UNIQUE, so ON CONFLICT replaces the
-- old read→create|update transaction. Timestamps come from the domain factory.
INSERT INTO app_settings (id, key, value, required_permission, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (key) DO UPDATE
    SET value               = EXCLUDED.value,
        required_permission = EXCLUDED.required_permission,
        updated_at          = EXCLUDED.updated_at
    RETURNING *;

-- name: GetPlatformSettingByKey :one
SELECT *
FROM app_settings
WHERE key = $1;

-- name: ListPlatformSettings :many
SELECT *
FROM app_settings
ORDER BY key;
