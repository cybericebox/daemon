-- name: GetPublishedInAppTemplate :one
SELECT *
FROM notification_in_app_templates
WHERE notification_type = $1
  AND status = 'published'
  AND (scope_event_id = sqlc.narg(scope_event_id) OR scope_event_id IS NULL)
ORDER BY scope_event_id IS NOT NULL DESC
LIMIT 1;

-- name: GetInAppTemplate :one
SELECT *
FROM notification_in_app_templates
WHERE id = $1;

-- name: ListInAppTemplates :many
SELECT *
FROM notification_in_app_templates
WHERE (sqlc.arg(type_filter)::text = '' OR notification_type = sqlc.arg(type_filter)::text)
  AND (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter)::text)
  AND scope_event_id IS NOT DISTINCT FROM sqlc.narg(scope_event_id)
ORDER BY notification_type, updated_at DESC;

-- name: CreateInAppTemplate :one
INSERT INTO notification_in_app_templates
(id, notification_type, status, title, body, link, icon, tone, accent_color, surface, auto_dismiss_ms, actions,
 dismissible, updated_by_user_id, scope_event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15) RETURNING *;

-- name: UpdateInAppTemplate :one
UPDATE notification_in_app_templates
SET title              = $2,
    body               = $3,
    link               = $4,
    icon               = $5,
    tone               = $6,
    accent_color       = $7,
    surface            = $8,
    auto_dismiss_ms    = $9,
    actions            = $10,
    dismissible        = $11,
    updated_by_user_id = $12,
    updated_at         = now()
WHERE id = $1
  AND status = 'draft' RETURNING *;

-- name: DeleteInAppTemplate :execrows
DELETE
FROM notification_in_app_templates
WHERE id = $1
  AND status = 'draft';

-- name: PublishInAppTemplate :one
WITH target AS (SELECT notification_type, scope_event_id
                FROM notification_in_app_templates
                WHERE notification_in_app_templates.id = sqlc.arg(id)
                  AND status = 'draft'),
     demoted AS (
UPDATE notification_in_app_templates
SET status     = 'unpublished',
    updated_at = now()
WHERE notification_type = (SELECT notification_type FROM target)
  AND scope_event_id IS NOT DISTINCT FROM (SELECT scope_event_id FROM target)
  AND status = 'published' RETURNING id)
UPDATE notification_in_app_templates
SET status             = 'published',
    published_at       = now(),
    updated_at         = now(),
    updated_by_user_id = sqlc.arg(updated_by_user_id)
WHERE notification_in_app_templates.id = sqlc.arg(id)
  AND status = 'draft'
  -- Forces Postgres to execute `demoted` to completion (including its
  -- immediate unique-index check) before this UPDATE runs. Without this data
  -- dependency the two writable CTEs have no ordering guarantee and can both
  -- touch the partial unique index "one published per (type, scope)" in the
  -- same instant, raising a spurious 23505 even though the end state is valid.
  AND (SELECT count(*) FROM demoted) IS NOT NULL RETURNING *;

-- name: RollbackInAppTemplate :one
WITH src AS (SELECT *
             FROM notification_in_app_templates
             WHERE notification_in_app_templates.id = sqlc.arg(source_id)
               AND status IN ('published', 'unpublished'))
INSERT
INTO notification_in_app_templates
(id, notification_type, status, title, body, link, icon, tone, accent_color, surface, auto_dismiss_ms, actions,
 dismissible, updated_by_user_id, scope_event_id)
SELECT sqlc.arg(new_id),
       notification_type,
       'draft',
       title,
       body,
       link,
       icon,
       tone,
       accent_color,
       surface,
       auto_dismiss_ms,
       actions,
       dismissible,
       sqlc.arg(updated_by_user_id),
       scope_event_id
FROM src
WHERE true
ON CONFLICT (notification_type, (COALESCE(scope_event_id, '00000000-0000-0000-0000-000000000000'::uuid)))
    WHERE status = 'draft'
DO UPDATE SET title = EXCLUDED.title,
              body = EXCLUDED.body,
              link = EXCLUDED.link,
              icon = EXCLUDED.icon,
              tone = EXCLUDED.tone,
              accent_color = EXCLUDED.accent_color,
              surface = EXCLUDED.surface,
              auto_dismiss_ms = EXCLUDED.auto_dismiss_ms,
              actions = EXCLUDED.actions,
              dismissible = EXCLUDED.dismissible,
              updated_by_user_id = EXCLUDED.updated_by_user_id,
              updated_at = now()
RETURNING *;

-- name: DeleteEventInAppTemplatesOfType :execrows
-- Resets an Event override: every Event row (any status) of the type goes, so
-- the Event falls back to the platform template.
DELETE
FROM notification_in_app_templates
WHERE scope_event_id = sqlc.arg(scope_event_id)::uuid
  AND notification_type = sqlc.arg(notification_type)::text;
