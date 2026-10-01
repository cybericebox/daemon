-- name: GetPublishedEmailTemplate :one
SELECT *
FROM notification_email_templates
WHERE notification_type = $1
  AND status = 'published'
  AND (scope_event_id = sqlc.narg(scope_event_id) OR scope_event_id IS NULL)
ORDER BY scope_event_id IS NOT NULL DESC
LIMIT 1;

-- name: GetEmailTemplate :one
SELECT *
FROM notification_email_templates
WHERE id = $1;

-- name: ListEmailTemplates :many
SELECT *
FROM notification_email_templates
WHERE (sqlc.arg(type_filter)::text = '' OR notification_type = sqlc.arg(type_filter)::text)
  AND (sqlc.arg(status_filter)::text = '' OR status = sqlc.arg(status_filter)::text)
  AND scope_event_id IS NOT DISTINCT FROM sqlc.narg(scope_event_id)
ORDER BY notification_type, updated_at DESC;

-- name: CreateEmailTemplate :one
INSERT INTO notification_email_templates
(id, notification_type, status, subject, preheader, body, styling, updated_by_user_id, scope_event_id)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING *;

-- name: UpdateEmailTemplate :one
UPDATE notification_email_templates
SET subject            = $2,
    preheader          = $3,
    body               = $4,
    styling            = $5,
    updated_by_user_id = $6,
    updated_at         = now()
WHERE id = $1
  AND status = 'draft' RETURNING *;

-- name: DeleteEmailTemplate :execrows
DELETE
FROM notification_email_templates
WHERE id = $1
  AND status = 'draft';

-- name: PublishEmailTemplate :one
WITH target AS (SELECT notification_type, scope_event_id
                FROM notification_email_templates
                WHERE notification_email_templates.id = sqlc.arg(id)
                  AND status = 'draft'),
     demoted AS (
UPDATE notification_email_templates
SET status     = 'unpublished',
    updated_at = now()
WHERE notification_type = (SELECT notification_type FROM target)
  AND scope_event_id IS NOT DISTINCT FROM (SELECT scope_event_id FROM target)
  AND status = 'published' RETURNING id)
UPDATE notification_email_templates
SET status             = 'published',
    published_at       = now(),
    updated_at         = now(),
    updated_by_user_id = sqlc.arg(updated_by_user_id)
WHERE notification_email_templates.id = sqlc.arg(id)
  AND status = 'draft'
  -- Forces Postgres to execute `demoted` to completion (including its
  -- immediate unique-index check) before this UPDATE runs. Without this data
  -- dependency the two writable CTEs have no ordering guarantee and can both
  -- touch the partial unique index "one published per (type, scope)" in the
  -- same instant, raising a spurious 23505 even though the end state is valid.
  AND (SELECT count(*) FROM demoted) IS NOT NULL RETURNING *;

-- name: RollbackEmailTemplate :one
WITH src AS (SELECT *
             FROM notification_email_templates
             WHERE notification_email_templates.id = sqlc.arg(source_id)
               AND status IN ('published', 'unpublished'))
INSERT
INTO notification_email_templates
(id, notification_type, status, subject, preheader, body, styling, updated_by_user_id, scope_event_id)
SELECT sqlc.arg(new_id),
       notification_type,
       'draft',
       subject,
       preheader,
       body,
       styling,
       sqlc.arg(updated_by_user_id),
       scope_event_id
FROM src
WHERE true
ON CONFLICT (notification_type, (COALESCE(scope_event_id, '00000000-0000-0000-0000-000000000000'::uuid)))
    WHERE status = 'draft'
DO UPDATE SET subject = EXCLUDED.subject,
              preheader = EXCLUDED.preheader,
              body = EXCLUDED.body,
              styling = EXCLUDED.styling,
              updated_by_user_id = EXCLUDED.updated_by_user_id,
              updated_at = now()
RETURNING *;

-- name: DeleteEventEmailTemplatesOfType :many
-- Resets an Event override: every Event row (any status) of the type goes, so
-- the Event falls back to the platform template. Returns the deleted ids for
-- file reference cleanup.
DELETE
FROM notification_email_templates
WHERE scope_event_id = sqlc.arg(scope_event_id)::uuid
  AND notification_type = sqlc.arg(notification_type)::text
RETURNING id;

-- name: EmailTemplateFileUsableByEvent :one
-- Whether an Event's email templates may use/serve the file: it must already
-- be referenced by a platform template (scope NULL), a block preset, or one of
-- THIS Event's own template rows — never by another Event's rows or by
-- unrelated owners (avatars, exercise images).
SELECT EXISTS(SELECT 1
              FROM file_references r
                       LEFT JOIN notification_email_templates t
                                 ON r.ref_type = sqlc.arg(template_ref_type)::text AND t.id = r.ref_id
              WHERE r.file_id = sqlc.arg(file_id)
                AND (r.ref_type = sqlc.arg(preset_ref_type)::text
                  OR (t.id IS NOT NULL AND
                      (t.scope_event_id IS NULL OR t.scope_event_id = sqlc.arg(event_id)::uuid))))::boolean;
