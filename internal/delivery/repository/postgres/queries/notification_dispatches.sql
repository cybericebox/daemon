-- name: CreateDispatch :one
INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, scope_event_id, broadcast_id)
VALUES ($1, $2, $3, $4, $5, $6) RETURNING *;

-- name: SetDispatchStatus :exec
UPDATE notification_dispatches
SET status     = $2,
    updated_at = now()
WHERE id = $1;

-- name: UpsertDispatchTarget :exec
-- attempts is the real number of delivery attempts of this run (dispatcher
-- rounds plus an SMTP fallback), added to any earlier run of the same target.
INSERT INTO notification_dispatch_targets (dispatch_id, channel, status, error, attempts, transport, recipient, fallback_error)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT (dispatch_id, channel) DO
UPDATE
    SET status = EXCLUDED.status,
    error = EXCLUDED.error,
    attempts = notification_dispatch_targets.attempts + EXCLUDED.attempts,
    transport = EXCLUDED.transport,
    recipient = EXCLUDED.recipient,
    fallback_error = EXCLUDED.fallback_error,
    updated_at = now();

-- name: GetDispatch :one
SELECT d.*, COALESCE(u.email, '')::text AS recipient_email,
       COALESCE(btrim(u.first_name || ' ' || u.last_name), '')::text AS recipient_name,
       COALESCE(e.name, '')::text AS event_name
FROM notification_dispatches d
LEFT JOIN users u ON u.id = d.recipient_user_id
LEFT JOIN events e ON e.id = d.scope_event_id
WHERE d.id = $1;

-- name: ListDispatches :many
-- Journal page. channel/result/transport filter on the per-channel targets:
-- a dispatch matches when one of its targets matches all of them. The
-- transport filter is one value or a comma-separated list.
SELECT d.*, COALESCE(u.email, '')::text AS recipient_email,
       COALESCE(btrim(u.first_name || ' ' || u.last_name), '')::text AS recipient_name,
       COALESCE(e.name, '')::text AS event_name
FROM notification_dispatches d
LEFT JOIN users u ON u.id = d.recipient_user_id
LEFT JOIN events e ON e.id = d.scope_event_id
WHERE (sqlc.arg(type_filter)::text = '' OR d.notification_type = sqlc.arg(type_filter)::text)
  AND (sqlc.arg(status_filter)::text = '' OR d.status = sqlc.arg(status_filter)::text)
  AND (sqlc.arg(user_filter)::text = '' OR d.recipient_user_id = sqlc.arg(user_filter)::uuid)
  AND (sqlc.arg(event_filter)::text = '' OR d.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid)
  AND (sqlc.arg(channel_filter)::text = '' AND sqlc.arg(result_filter)::text = '' AND sqlc.arg(transport_filter)::text = ''
       OR EXISTS (SELECT 1 FROM notification_dispatch_targets t
                  WHERE t.dispatch_id = d.id
                    AND (sqlc.arg(channel_filter)::text = '' OR t.channel = sqlc.arg(channel_filter)::text)
                    AND (sqlc.arg(result_filter)::text = '' OR t.status = sqlc.arg(result_filter)::text)
                    AND (sqlc.arg(transport_filter)::text = '' OR t.transport = ANY(string_to_array(sqlc.arg(transport_filter)::text, ',')))))
  AND (sqlc.arg(cursor_filter)::text = '' OR (d.created_at, d.id) < (
    SELECT created_at, id FROM notification_dispatches WHERE id = NULLIF(sqlc.arg(cursor_filter)::text, '')::uuid
  ))
ORDER BY d.created_at DESC, d.id DESC LIMIT sqlc.arg(limit_val);

-- name: CountDispatches :one
SELECT count(*)
FROM notification_dispatches d
WHERE (sqlc.arg(type_filter)::text = '' OR d.notification_type = sqlc.arg(type_filter)::text)
  AND (sqlc.arg(status_filter)::text = '' OR d.status = sqlc.arg(status_filter)::text)
  AND (sqlc.arg(user_filter)::text = '' OR d.recipient_user_id = sqlc.arg(user_filter)::uuid)
  AND (sqlc.arg(event_filter)::text = '' OR d.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid)
  AND (sqlc.arg(channel_filter)::text = '' AND sqlc.arg(result_filter)::text = '' AND sqlc.arg(transport_filter)::text = ''
       OR EXISTS (SELECT 1 FROM notification_dispatch_targets t
                  WHERE t.dispatch_id = d.id
                    AND (sqlc.arg(channel_filter)::text = '' OR t.channel = sqlc.arg(channel_filter)::text)
                    AND (sqlc.arg(result_filter)::text = '' OR t.status = sqlc.arg(result_filter)::text)
                    AND (sqlc.arg(transport_filter)::text = '' OR t.transport = ANY(string_to_array(sqlc.arg(transport_filter)::text, ',')))));

-- name: ListDispatchTargets :many
SELECT *
FROM notification_dispatch_targets
WHERE dispatch_id = $1
ORDER BY channel;

-- name: ListDispatchTargetsByDispatches :many
SELECT *
FROM notification_dispatch_targets
WHERE dispatch_id = ANY(sqlc.arg(dispatch_ids)::uuid[])
ORDER BY dispatch_id, channel;

-- name: CountDispatchesByStatusSince :many
SELECT status, count(*) ::bigint AS count
FROM notification_dispatches
WHERE created_at >= $1
GROUP BY status;

-- name: CountDispatchesByTypeSince :many
SELECT notification_type, count(*) ::bigint AS count
FROM notification_dispatches
WHERE created_at >= $1
GROUP BY notification_type;

-- name: CountTargetsByChannelStatusSince :many
SELECT t.channel, t.status, count(*) ::bigint AS count
FROM notification_dispatch_targets t
    JOIN notification_dispatches d
ON d.id = t.dispatch_id
WHERE d.created_at >= $1
GROUP BY t.channel, t.status;

-- name: CountEmailDeliveredSince :one
-- Messages delivered through one SMTP transport since a moment (the rolling
-- daily quota). event_id narrows the count to one Event's own transport; the
-- platform and env transports count every dispatch that went through them,
-- including Event mail that fell back to the platform.
SELECT count(*)::bigint
FROM notification_dispatch_targets t
WHERE t.channel = 'email'
  AND t.status = 'done'
  AND t.transport = sqlc.arg(transport)::text
  AND t.updated_at >= sqlc.arg(since)::timestamptz
  AND (sqlc.narg(event_id)::uuid IS NULL
       OR EXISTS (SELECT 1 FROM notification_dispatches d
                  WHERE d.id = t.dispatch_id AND d.scope_event_id = sqlc.narg(event_id)::uuid));
