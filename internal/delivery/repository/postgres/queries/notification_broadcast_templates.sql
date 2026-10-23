-- name: CreateNotificationBroadcastTemplate :one
INSERT INTO notification_broadcast_templates (id, scope_event_id, name, channels, subject, preheader, email_body,
                                              email_styling, inapp_title, inapp_body, inapp_link, created_by, updated_by)
VALUES (sqlc.arg(id), sqlc.narg(scope_event_id), sqlc.arg(name), sqlc.arg(channels)::text[], sqlc.arg(subject),
        sqlc.arg(preheader), sqlc.arg(email_body), sqlc.arg(email_styling), sqlc.arg(inapp_title),
        sqlc.arg(inapp_body), sqlc.arg(inapp_link), sqlc.narg(actor), sqlc.narg(actor))
RETURNING id;

-- name: UpdateNotificationBroadcastTemplate :execrows
-- Only within its own scope: scope_filter is 'platform' or the Event id.
UPDATE notification_broadcast_templates
SET name          = sqlc.arg(name),
    channels      = sqlc.arg(channels)::text[],
    subject       = sqlc.arg(subject),
    preheader     = sqlc.arg(preheader),
    email_body    = sqlc.arg(email_body),
    email_styling = sqlc.arg(email_styling),
    inapp_title   = sqlc.arg(inapp_title),
    inapp_body    = sqlc.arg(inapp_body),
    inapp_link    = sqlc.arg(inapp_link),
    updated_by    = sqlc.narg(actor),
    updated_at    = now()
WHERE id = sqlc.arg(id)
  AND ((sqlc.arg(scope_filter)::text = 'platform' AND scope_event_id IS NULL)
    OR scope_event_id::text = sqlc.arg(scope_filter)::text);

-- name: DeleteNotificationBroadcastTemplate :execrows
DELETE
FROM notification_broadcast_templates
WHERE id = sqlc.arg(id)
  AND ((sqlc.arg(scope_filter)::text = 'platform' AND scope_event_id IS NULL)
    OR scope_event_id::text = sqlc.arg(scope_filter)::text);

-- name: ListNotificationBroadcastTemplates :many
-- scope_filter: 'platform' = platform templates, otherwise the Event id.
-- Recently edited first.
SELECT t.*,
       COALESCE(NULLIF(btrim(u.first_name || ' ' || u.last_name), ''), u.email, '')::text AS updated_by_name
FROM notification_broadcast_templates t
LEFT JOIN users u ON u.id = t.updated_by
WHERE (sqlc.arg(scope_filter)::text = 'platform' AND t.scope_event_id IS NULL)
   OR t.scope_event_id::text = sqlc.arg(scope_filter)::text
ORDER BY t.updated_at DESC, t.id DESC
LIMIT sqlc.arg(limit_val);

-- name: GetNotificationBroadcastTemplate :one
SELECT t.*,
       COALESCE(NULLIF(btrim(u.first_name || ' ' || u.last_name), ''), u.email, '')::text AS updated_by_name
FROM notification_broadcast_templates t
LEFT JOIN users u ON u.id = t.updated_by
WHERE t.id = sqlc.arg(id)
  AND ((sqlc.arg(scope_filter)::text = 'platform' AND t.scope_event_id IS NULL)
    OR t.scope_event_id::text = sqlc.arg(scope_filter)::text);
