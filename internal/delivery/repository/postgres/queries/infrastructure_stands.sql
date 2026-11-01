-- name: ListPlatformStands :many
-- Stands of every event. statuses is NULL for all statuses; kind is '' for all
-- kinds, 'event' for event teams or 'moderators' for the moderators team.
SELECT s.event_id, e.name AS event_name, e.tag AS event_tag, s.event_team_id, t.moderators,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       s.status, COALESCE(s.reason, '')::text AS reason, s.updated_at, s.status_changed_at, s.generation,
       count(*) OVER ()::bigint AS total
FROM event_team_stands s
JOIN events e ON e.id = s.event_id
JOIN event_teams t ON t.id = s.event_team_id
WHERE (sqlc.narg(event_id)::uuid IS NULL OR s.event_id = sqlc.narg(event_id)::uuid)
  AND (sqlc.narg(statuses)::smallint[] IS NULL OR s.status = ANY(sqlc.narg(statuses)::smallint[]))
  AND (sqlc.arg(kind)::text = '' OR (sqlc.arg(kind)::text = 'moderators') = t.moderators)
  AND (sqlc.arg(search)::text = ''
    OR e.name ILIKE '%' || sqlc.arg(search)::text || '%'
    OR e.tag ILIKE '%' || sqlc.arg(search)::text || '%'
    OR event_team_public_name(t.individual, t.event_id, t.captain_id, t.name) ILIKE '%' || sqlc.arg(search)::text || '%')
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'event' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(e.name) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'event' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(e.name) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'team' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'team' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'asc' THEN s.status END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'desc' THEN s.status END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'reason' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(COALESCE(s.reason, '')) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'reason' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(COALESCE(s.reason, '')) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'updatedAt' AND sqlc.arg(sort_dir)::text = 'asc' THEN s.updated_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'updatedAt' AND sqlc.arg(sort_dir)::text = 'desc' THEN s.updated_at END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'generation' AND sqlc.arg(sort_dir)::text = 'asc' THEN s.generation END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'generation' AND sqlc.arg(sort_dir)::text = 'desc' THEN s.generation END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'statusChangedAt' AND sqlc.arg(sort_dir)::text = 'asc' THEN s.status_changed_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'statusChangedAt' AND sqlc.arg(sort_dir)::text = 'desc' THEN s.status_changed_at END DESC,
  s.updated_at DESC, s.event_team_id
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: ListPlatformStandEvents :many
SELECT e.id, e.name, e.tag
FROM events e
WHERE EXISTS (SELECT 1 FROM event_team_stands s WHERE s.event_id = e.id)
ORDER BY e.name, e.id;

-- name: CountPlatformStandsByStatus :many
SELECT status, count(*)::bigint AS total
FROM event_team_stands
GROUP BY status;

-- name: CountPlatformStandsByKindStatus :many
-- The stand counts split by kind: moderators = the moderators team stand, else an event team stand.
SELECT t.moderators, s.status, count(*)::bigint AS total
FROM event_team_stands s
JOIN event_teams t ON t.id = s.event_team_id
GROUP BY t.moderators, s.status;
