-- name: GetNotificationSetting :one
SELECT *
FROM notification_settings
WHERE notification_type = $1
  AND channel = $2;

-- name: ListNotificationSettings :many
SELECT *
FROM notification_settings
ORDER BY notification_type, channel;

-- name: UpsertNotificationSetting :one
INSERT INTO notification_settings (notification_type, channel, enabled, user_can_change, user_default)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT (notification_type, channel) DO
UPDATE
    SET enabled = EXCLUDED.enabled,
    user_can_change = EXCLUDED.user_can_change,
    user_default = EXCLUDED.user_default
    RETURNING *;

-- name: UpsertUserSetting :exec
INSERT INTO notification_user_settings (user_id, notification_type, channel, enabled)
VALUES ($1, $2, $3, $4) ON CONFLICT (user_id, notification_type, channel) DO
UPDATE
    SET enabled = EXCLUDED.enabled;

-- name: ListUserSettings :many
SELECT *
FROM notification_user_settings
WHERE user_id = $1
ORDER BY notification_type, channel;

-- name: GetActiveChannels :many
SELECT g.channel
FROM notification_settings g
         LEFT JOIN notification_user_settings u
                   ON u.notification_type = g.notification_type
                       AND u.channel = g.channel
                       AND u.user_id = $1
WHERE g.notification_type = $2
  AND g.enabled
  AND (NOT g.user_can_change OR COALESCE(u.enabled, g.user_default))
ORDER BY g.channel;