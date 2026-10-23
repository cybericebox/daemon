-- name: GetUserUIPreference :one
SELECT value
FROM user_ui_preferences
WHERE user_id = sqlc.arg(user_id)
  AND key = sqlc.arg(key);

-- name: UpsertUserUIPreference :exec
INSERT INTO user_ui_preferences (user_id, key, value, updated_at)
VALUES (sqlc.arg(user_id), sqlc.arg(key), sqlc.arg(value), now())
ON CONFLICT (user_id, key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at;
