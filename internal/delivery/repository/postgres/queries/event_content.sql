-- name: GetEventContentSettings :one
SELECT id, landing_document, landing_draft, live_layout, live_layout_draft
FROM events
WHERE id = sqlc.arg(event_id);

-- name: SaveEventLandingDraft :execrows
UPDATE events
SET landing_draft = sqlc.arg(landing_draft)
WHERE id = sqlc.arg(event_id);

-- Publishes the draft the use case read and validated. A draft saved in the
-- meantime differs from it and stays as the pending draft.
-- name: PublishEventLandingDraft :execrows
UPDATE events
SET landing_document = sqlc.arg(landing_document),
    landing_draft = CASE WHEN landing_draft = sqlc.arg(landing_document)::jsonb THEN NULL ELSE landing_draft END
WHERE id = sqlc.arg(event_id) AND landing_draft IS NOT NULL;

-- name: DiscardEventLandingDraft :execrows
UPDATE events
SET landing_draft = NULL
WHERE id = sqlc.arg(event_id) AND landing_draft IS NOT NULL;

-- name: SaveEventLiveLayoutDraft :execrows
UPDATE events
SET live_layout_draft = sqlc.arg(live_layout_draft)
WHERE id = sqlc.arg(event_id);

-- name: PublishEventLiveLayout :one
UPDATE events
SET live_layout = jsonb_set(live_layout_draft, '{version}', to_jsonb(((live_layout->>'version')::bigint + 1))),
    live_layout_draft = NULL
WHERE id = sqlc.arg(event_id) AND live_layout_draft IS NOT NULL
RETURNING live_layout;

-- A new page is unpublished: its columns mirror the draft (reserving the slug).
-- name: CreateEventPage :one
INSERT INTO event_pages (id, event_id, slug, title, document, visibility, navigation, navigation_order, draft, published_at, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(slug), sqlc.arg(title), sqlc.arg(document), sqlc.arg(visibility), sqlc.arg(navigation), sqlc.arg(navigation_order), sqlc.arg(draft), NULL, sqlc.arg(created_at), sqlc.arg(updated_at))
RETURNING *;

-- name: GetEventPageBySlug :one
SELECT *
FROM event_pages
WHERE event_id = sqlc.arg(event_id) AND slug = sqlc.arg(slug);

-- name: ListEventPages :many
SELECT *
FROM event_pages
WHERE event_id = sqlc.arg(event_id)
ORDER BY navigation_order ASC, id ASC;

-- Saves the editor's draft. A page that was never published also mirrors the
-- draft into its columns, so its slug stays reserved by the unique index.
-- name: SaveEventPageDraft :one
UPDATE event_pages
SET draft = sqlc.arg(draft),
    slug = CASE WHEN published_at IS NULL THEN sqlc.arg(slug) ELSE slug END,
    title = CASE WHEN published_at IS NULL THEN sqlc.arg(title) ELSE title END,
    document = CASE WHEN published_at IS NULL THEN sqlc.arg(document) ELSE document END,
    visibility = CASE WHEN published_at IS NULL THEN sqlc.arg(visibility) ELSE visibility END,
    navigation = CASE WHEN published_at IS NULL THEN sqlc.arg(navigation) ELSE navigation END,
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND event_id = sqlc.arg(event_id)
RETURNING *;

-- name: DiscardEventPageDraft :execrows
UPDATE event_pages
SET draft = NULL, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND event_id = sqlc.arg(event_id) AND draft IS NOT NULL AND published_at IS NOT NULL;

-- name: DeleteEventPage :execrows
DELETE FROM event_pages
WHERE id = sqlc.arg(id) AND event_id = sqlc.arg(event_id);
