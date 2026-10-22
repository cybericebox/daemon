-- name: ListEventLiveScreens :many
-- The event's screens in the editor order; the first one is «Екран 1» of the
-- old single-screen routes.
SELECT *
FROM event_live_screens
WHERE event_id = sqlc.arg(event_id)
ORDER BY position, id;

-- name: GetEventLiveScreen :one
SELECT *
FROM event_live_screens
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: GetFirstEventLiveScreen :one
SELECT *
FROM event_live_screens
WHERE event_id = sqlc.arg(event_id)
ORDER BY position, id
LIMIT 1;

-- name: LockEventForLiveScreenChange :one
-- Serializes adding and deleting screens of one event (the limit and the
-- last screen are checked under it).
SELECT id
FROM events
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: CountEventLiveScreens :one
SELECT count(*)
FROM event_live_screens
WHERE event_id = sqlc.arg(event_id);

-- name: CreateEventLiveScreen :one
-- A new screen goes last.
INSERT INTO event_live_screens (id, event_id, position, name, layout, layout_draft, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id),
        (SELECT coalesce(max(s.position) + 1, 0) FROM event_live_screens AS s WHERE s.event_id = sqlc.arg(event_id)),
        sqlc.arg(name), sqlc.arg(layout), sqlc.narg(layout_draft), sqlc.arg(created_at), sqlc.arg(created_at))
RETURNING *;

-- name: RenameEventLiveScreen :one
UPDATE event_live_screens
SET name       = sqlc.arg(name),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
RETURNING *;

-- name: SetEventLiveScreenPosition :execrows
UPDATE event_live_screens
SET position = sqlc.arg(position)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: SaveEventLiveScreenDraft :execrows
UPDATE event_live_screens
SET layout_draft = sqlc.arg(layout_draft),
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: PublishEventLiveScreen :one
-- The draft becomes the published layout with the next version, which the
-- open screens poll for.
UPDATE event_live_screens
SET layout       = jsonb_set(layout_draft, '{version}', to_jsonb(((layout ->> 'version')::bigint + 1))),
    layout_draft = NULL,
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND layout_draft IS NOT NULL
RETURNING layout;

-- name: DeleteEventLiveScreen :execrows
-- Its links go with it (ON DELETE CASCADE), so its URL stops working.
DELETE
FROM event_live_screens
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);
