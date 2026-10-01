-- name: CreateNotificationBroadcast :one
INSERT INTO notification_broadcasts (id, scope_event_id, created_by, channels, subject, preheader, email_body, email_styling,
                                     inapp_title, inapp_body, inapp_link, audience, recipient_count, status)
VALUES (sqlc.arg(id), sqlc.narg(scope_event_id), sqlc.narg(created_by), sqlc.arg(channels)::text[], sqlc.arg(subject),
        sqlc.arg(preheader), sqlc.arg(email_body), sqlc.arg(email_styling), sqlc.arg(inapp_title), sqlc.arg(inapp_body),
        sqlc.arg(inapp_link), sqlc.arg(audience), sqlc.arg(recipient_count), 'sending')
RETURNING *;

-- name: GetNotificationBroadcast :one
SELECT b.*,
       COALESCE(NULLIF(btrim(u.first_name || ' ' || u.last_name), ''), u.email, '')::text AS created_by_name,
       COALESCE(e.name, '')::text                                                          AS event_name,
       (SELECT count(*) FROM notification_dispatches d
        WHERE d.broadcast_id = b.id AND EXISTS (SELECT 1 FROM notification_dispatch_targets t
                                                WHERE t.dispatch_id = d.id AND t.status = 'error'))::bigint AS failed_count,
       (SELECT count(*) FROM notification_dispatches d
        WHERE d.broadcast_id = b.id
          AND d.status = 'done'
          AND NOT EXISTS (SELECT 1 FROM notification_dispatch_targets t
                          WHERE t.dispatch_id = d.id AND t.status = 'error'))::bigint AS sent_count
FROM notification_broadcasts b
LEFT JOIN users u ON u.id = b.created_by
LEFT JOIN events e ON e.id = b.scope_event_id
WHERE b.id = sqlc.arg(id);

-- name: ListNotificationBroadcasts :many
-- scope_filter: '' = every scope, 'platform' = platform broadcasts only,
-- otherwise the Event id.
SELECT b.*,
       COALESCE(NULLIF(btrim(u.first_name || ' ' || u.last_name), ''), u.email, '')::text AS created_by_name,
       COALESCE(e.name, '')::text                                                          AS event_name,
       (SELECT count(*) FROM notification_dispatches d
        WHERE d.broadcast_id = b.id AND EXISTS (SELECT 1 FROM notification_dispatch_targets t
                                                WHERE t.dispatch_id = d.id AND t.status = 'error'))::bigint AS failed_count,
       (SELECT count(*) FROM notification_dispatches d
        WHERE d.broadcast_id = b.id
          AND d.status = 'done'
          AND NOT EXISTS (SELECT 1 FROM notification_dispatch_targets t
                          WHERE t.dispatch_id = d.id AND t.status = 'error'))::bigint AS sent_count
FROM notification_broadcasts b
LEFT JOIN users u ON u.id = b.created_by
LEFT JOIN events e ON e.id = b.scope_event_id
WHERE (sqlc.arg(scope_filter)::text = ''
    OR (sqlc.arg(scope_filter)::text = 'platform' AND b.scope_event_id IS NULL)
    OR b.scope_event_id::text = sqlc.arg(scope_filter)::text)
  AND (sqlc.arg(cursor_filter)::text = '' OR (b.created_at, b.id) < (
    SELECT created_at, id FROM notification_broadcasts WHERE id = NULLIF(sqlc.arg(cursor_filter)::text, '')::uuid
  ))
ORDER BY b.created_at DESC, b.id DESC
LIMIT sqlc.arg(limit_val);

-- name: FinishNotificationBroadcast :exec
UPDATE notification_broadcasts
SET status          = sqlc.arg(status),
    recipient_count = sqlc.arg(recipient_count),
    finished_at     = now()
WHERE id = sqlc.arg(id);

-- name: ListBroadcastQueuedUserIDs :many
SELECT recipient_user_id
FROM notification_dispatches
WHERE broadcast_id = sqlc.arg(broadcast_id);

-- name: ListBroadcastDeliveries :many
-- One row per recipient and channel target of the broadcast (recipients still
-- waiting for their first attempt have a NULL channel).
SELECT d.id                             AS dispatch_id,
       d.recipient_user_id,
       COALESCE(u.email, '')::text      AS recipient_email,
       d.status                         AS dispatch_status,
       COALESCE(t.channel, '')::text    AS channel,
       COALESCE(t.status, '')::text     AS target_status,
       COALESCE(t.error, '')::text      AS error
FROM notification_dispatches d
LEFT JOIN users u ON u.id = d.recipient_user_id
LEFT JOIN notification_dispatch_targets t ON t.dispatch_id = d.id
WHERE d.broadcast_id = sqlc.arg(broadcast_id)
ORDER BY (t.status = 'error') DESC NULLS LAST, d.created_at, d.id, t.channel
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: ListPlatformBroadcastAudience :many
-- kind: all | roles | users. Only active, non-deleted accounts with an email.
SELECT id, email, first_name, last_name
FROM users
WHERE status = 'active'
  AND deleted_at IS NULL
  AND email <> ''
  AND (sqlc.arg(kind)::text = 'all'
    OR (sqlc.arg(kind)::text = 'roles' AND role = ANY(sqlc.arg(roles)::text[]))
    OR (sqlc.arg(kind)::text = 'users' AND id = ANY(sqlc.arg(user_ids)::uuid[])))
ORDER BY id;

-- name: ListEventBroadcastAudience :many
-- kind: all_participants (approved and pending) | approved | pending |
-- captains | teams | participants | staff.
SELECT u.id, u.email, u.first_name, u.last_name
FROM users u
WHERE u.status = 'active'
  AND u.deleted_at IS NULL
  AND u.email <> ''
  AND (
    (sqlc.arg(kind)::text = 'staff'
        AND EXISTS (SELECT 1 FROM event_managers m WHERE m.event_id = sqlc.arg(event_id) AND m.user_id = u.id))
    OR (sqlc.arg(kind)::text <> 'staff' AND EXISTS (
        SELECT 1 FROM event_participants p
        WHERE p.event_id = sqlc.arg(event_id)
          AND p.user_id = u.id
          AND CASE sqlc.arg(kind)::text
                  WHEN 'all_participants' THEN p.status IN (1, 2)
                  WHEN 'approved' THEN p.status = 2
                  WHEN 'pending' THEN p.status = 1
                  WHEN 'captains' THEN p.status = 2 AND p.team_role = 0
                  WHEN 'teams' THEN p.status = 2 AND p.team_id = ANY(sqlc.arg(team_ids)::uuid[])
                  WHEN 'participants' THEN p.status IN (1, 2) AND p.user_id = ANY(sqlc.arg(user_ids)::uuid[])
                  ELSE false
              END))
  )
ORDER BY u.id;
