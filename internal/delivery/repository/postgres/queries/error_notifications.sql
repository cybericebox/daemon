-- name: EnqueueErrorNotification :execrows
WITH claimed AS (
 UPDATE error_groups SET notification_enqueued_at = sqlc.arg(now)::timestamptz
 WHERE id = sqlc.arg(group_id)::uuid
 AND (notification_enqueued_at IS NULL OR notification_enqueued_at <= sqlc.arg(cutoff)::timestamptz)
 AND NOT EXISTS (SELECT 1 FROM error_notification_deliveries d WHERE d.group_id = error_groups.id AND d.delivered_at IS NULL AND d.cancelled_at IS NULL)
 RETURNING id
)
INSERT INTO error_notification_deliveries(id, group_id, channel, body, email_body, created_at, next_attempt_at)
SELECT sqlc.arg(id)::uuid, id, 'prepare', sqlc.arg(body)::text, sqlc.arg(email_body)::text, sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz FROM claimed;

-- name: GetErrorNotification :one
SELECT * FROM error_notification_deliveries WHERE id = $1;

-- name: LockErrorNotification :one
SELECT * FROM error_notification_deliveries WHERE id = $1 FOR UPDATE;

-- name: StartErrorNotification :exec
UPDATE error_notification_deliveries SET attempts = attempts + 1, last_attempt_at = $2 WHERE id = $1;

-- name: AddErrorNotificationTarget :execrows
INSERT INTO error_notification_deliveries(id, group_id, batch_id, channel, target, label, body, created_at, next_attempt_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $8)
ON CONFLICT (batch_id, channel, target) DO NOTHING;

-- name: FinishErrorNotification :execrows
WITH finished AS (
 UPDATE error_notification_deliveries SET last_error = sqlc.arg(last_error)::text,
 next_attempt_at = sqlc.arg(next_attempt_at)::timestamptz,
 delivered_at = sqlc.narg(delivered_at)::timestamptz, cancelled_at = sqlc.narg(cancelled_at)::timestamptz
 WHERE id = sqlc.arg(id)::uuid AND delivered_at IS NULL
 RETURNING group_id, channel, delivered_at
)
UPDATE error_groups SET last_notified_at = finished.delivered_at, suppressed_since = 0
FROM finished WHERE error_groups.id = finished.group_id AND finished.channel <> 'prepare' AND finished.delivered_at IS NOT NULL;

-- name: ListErrorNotificationDeliveries :many
SELECT * FROM error_notification_deliveries
WHERE group_id = $1 AND (channel <> 'prepare' OR delivered_at IS NULL)
ORDER BY created_at DESC, id DESC LIMIT 100;
