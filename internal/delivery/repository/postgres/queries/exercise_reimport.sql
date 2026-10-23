-- name: ListEditableImportTargets :many
-- Apply the exact write policy and archive freeze BEFORE keyset pagination.
SELECT e.id, COALESCE(v.name, e.name)::text AS name, e.scope, e.owner_event_id,
       COALESCE(NULLIF(ev.internal_name, ''), ev.name, '')::text AS owner_event_name,
       COALESCE(ev.tag, '')::text AS owner_event_tag,
       (e.draft_version_id IS NOT NULL)::boolean AS has_draft
FROM exercises e
LEFT JOIN exercise_versions v ON v.id = COALESCE(e.draft_version_id, e.published_version_id)
LEFT JOIN events ev ON ev.id = e.owner_event_id
WHERE e.archived_at IS NULL
  AND (sqlc.arg(writer_all)::boolean OR (e.scope = 1 AND EXISTS (
      SELECT 1 FROM event_managers member
      WHERE member.event_id = e.owner_event_id AND member.user_id = sqlc.arg(viewer_id)
        AND member.role IN (0, 1))))
  AND (sqlc.arg(search)::text = '' OR COALESCE(v.name, e.name) ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.narg(cursor_id)::uuid IS NULL OR e.id < sqlc.narg(cursor_id)::uuid)
ORDER BY e.id DESC
LIMIT sqlc.arg(limit_val);

-- name: CountEditableImportTargets :one
SELECT count(*) FROM exercises e
LEFT JOIN exercise_versions v ON v.id = COALESCE(e.draft_version_id, e.published_version_id)
WHERE e.archived_at IS NULL
  AND (sqlc.arg(writer_all)::boolean OR (e.scope = 1 AND EXISTS (
      SELECT 1 FROM event_managers member
      WHERE member.event_id = e.owner_event_id AND member.user_id = sqlc.arg(viewer_id)
        AND member.role IN (0, 1))))
  AND (sqlc.arg(search)::text = '' OR COALESCE(v.name, e.name) ILIKE '%' || sqlc.arg(search)::text || '%');

-- name: GetImportTargetContext :one
-- Caller has already checked ActionWrite and the archive freeze for this ID.
SELECT COALESCE(NULLIF(ev.internal_name, ''), ev.name, '')::text AS owner_event_name,
       COALESCE(ev.tag, '')::text AS owner_event_tag
FROM exercises e LEFT JOIN events ev ON ev.id = e.owner_event_id
WHERE e.id = $1;
