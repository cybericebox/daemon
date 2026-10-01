-- name: CreateSiteBanner :one
INSERT INTO site_banners (id, scope_event_id, text, link_url, link_label, level, active_from, active_to, dismissible,
                          audience, is_active, created_by)
VALUES (sqlc.arg(id), sqlc.narg(scope_event_id), sqlc.arg(text), sqlc.arg(link_url), sqlc.arg(link_label),
        sqlc.arg(level), sqlc.narg(active_from), sqlc.narg(active_to), sqlc.arg(dismissible), sqlc.arg(audience),
        sqlc.arg(is_active), sqlc.narg(created_by))
RETURNING *;

-- name: UpdateSiteBanner :one
UPDATE site_banners
SET text        = sqlc.arg(text),
    link_url    = sqlc.arg(link_url),
    link_label  = sqlc.arg(link_label),
    level       = sqlc.arg(level),
    active_from = sqlc.narg(active_from),
    active_to   = sqlc.narg(active_to),
    dismissible = sqlc.arg(dismissible),
    audience    = sqlc.arg(audience),
    is_active   = sqlc.arg(is_active),
    updated_at  = now()
WHERE id = sqlc.arg(id)
RETURNING *;

-- name: GetSiteBanner :one
SELECT *
FROM site_banners
WHERE id = sqlc.arg(id);

-- name: DeleteSiteBanner :execrows
DELETE
FROM site_banners
WHERE id = sqlc.arg(id);

-- name: ListSiteBanners :many
-- scope_filter: 'platform' = platform banners, otherwise the Event id.
SELECT *
FROM site_banners
WHERE (sqlc.arg(scope_filter)::text = 'platform' AND scope_event_id IS NULL)
   OR scope_event_id::text = sqlc.arg(scope_filter)::text
ORDER BY created_at DESC, id DESC;

-- name: ListVisibleSiteBanners :many
-- Banners a viewer sees: active, inside the window, platform ones plus those
-- of event_filter ('' = no Event site). user_id is NULL for an anonymous
-- viewer; a participant is an approved participant or a manager of the Event.
SELECT b.*
FROM site_banners b
WHERE b.is_active
  AND (b.active_from IS NULL OR b.active_from <= now())
  AND (b.active_to IS NULL OR b.active_to > now())
  AND (b.scope_event_id IS NULL
    OR (sqlc.arg(event_filter)::text <> '' AND b.scope_event_id::text = sqlc.arg(event_filter)::text))
  AND (b.audience = 'everyone'
    OR (b.audience = 'signed_in' AND sqlc.narg(user_id)::uuid IS NOT NULL)
    OR (b.audience = 'participants' AND sqlc.narg(user_id)::uuid IS NOT NULL AND (
        EXISTS (SELECT 1 FROM event_participants p
                WHERE p.event_id = b.scope_event_id AND p.user_id = sqlc.narg(user_id)::uuid AND p.status = 2)
        OR EXISTS (SELECT 1 FROM event_managers m
                   WHERE m.event_id = b.scope_event_id AND m.user_id = sqlc.narg(user_id)::uuid))))
ORDER BY (b.level = 'critical') DESC, (b.level = 'warning') DESC, b.created_at DESC, b.id DESC;
