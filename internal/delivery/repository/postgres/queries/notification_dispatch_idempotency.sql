-- name: CreateDispatchIfAbsent :execrows
INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, scope_event_id, broadcast_id)
VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (id) DO NOTHING;
