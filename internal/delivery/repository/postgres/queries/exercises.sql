-- name: CreateExercise :one
-- Timestamps come from the domain factory, not DB defaults.
INSERT INTO exercises (id, name, description, tags, created_at, created_by, updated_at, updated_by,
                       scope, owner_event_id, access_level, origin_event_id, forked_from_exercise_id, forked_from_version_id)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(description), sqlc.arg(tags), sqlc.arg(created_at), sqlc.arg(created_by),
        sqlc.arg(updated_at), sqlc.arg(updated_by), sqlc.arg(scope), sqlc.narg(owner_event_id), sqlc.arg(access_level),
        sqlc.narg(origin_event_id), sqlc.narg(forked_from_exercise_id), sqlc.narg(forked_from_version_id))
RETURNING *;

-- name: GetExerciseByID :one
SELECT *
FROM exercises
WHERE id = $1;

-- name: UpdateExercise :execrows
-- Whole-identity write. Deliberately excludes draft_version_id /
-- published_version_id (owned by the lifecycle CTEs — including them here
-- would race a concurrent publish) and created_at/created_by (immutable).
UPDATE exercises
SET name        = sqlc.arg(name),
    description = sqlc.arg(description),
    tags        = sqlc.arg(tags),
    archived_at = sqlc.narg(archived_at),
    access_level = sqlc.arg(access_level),
    updated_at  = sqlc.arg(updated_at),
    updated_by  = sqlc.arg(updated_by)
WHERE id = sqlc.arg(id)
  -- Optimistic lock: 0 rows means "modified since read" (or gone).
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: DeleteExercise :execrows
DELETE
FROM exercises
WHERE id = $1;

-- Shared list filters (keep the four list/count queries in sync):
-- scope '' | catalog | event; event_ids keeps exercises relevant to ANY of
-- the events (owned by one, or a catalog exercise available to one) — with
-- scope 'catalog' that is "available to any", with 'event' "owned by any";
-- viewer_id set = a non-admin who sees only what they may read.

-- name: ListExercisesCursor :many
SELECT sqlc.embed(exercises),
       exercise_status(draft_version_id, published_version_id, archived_at IS NOT NULL,
                       sqlc.narg(viewer_id)::uuid IS NULL OR scope = 1)::text AS status
FROM exercises
WHERE (sqlc.arg(search)::text = '' OR name ILIKE '%' || sqlc.arg(search)::text || '%'
    OR description ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (cardinality(sqlc.arg(tags)::text[]) = 0 OR tags && sqlc.arg(tags)::text[])
  AND (CASE WHEN sqlc.arg(archived)::text = 'only' THEN archived_at IS NOT NULL ELSE archived_at IS NULL END)
  AND (sqlc.arg(scope)::text = '' OR (sqlc.arg(scope)::text = 'catalog' AND scope = 0)
    OR (sqlc.arg(scope)::text = 'event' AND scope = 1))
  AND (COALESCE(cardinality(sqlc.arg(event_ids)::uuid[]), 0) = 0 OR owner_event_id = ANY (sqlc.arg(event_ids)::uuid[])
    OR (scope = 0 AND EXISTS (SELECT 1
                              FROM unnest(sqlc.arg(event_ids)::uuid[]) AS selected(event_id)
                              WHERE exercise_available_to_event(exercises.id, selected.event_id))))
  AND (sqlc.arg(infrastructure)::text = ''
    OR exercise_has_infrastructure(id) = (sqlc.arg(infrastructure)::text = 'yes'))
  AND (sqlc.narg(viewer_id)::uuid IS NULL OR exercise_readable_by(id, sqlc.narg(viewer_id)::uuid))
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_val);

-- name: CountExercises :one
SELECT count(*)
FROM exercises
WHERE (sqlc.arg(search)::text = '' OR name ILIKE '%' || sqlc.arg(search)::text || '%'
    OR description ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (cardinality(sqlc.arg(tags)::text[]) = 0 OR tags && sqlc.arg(tags)::text[])
  AND (CASE WHEN sqlc.arg(archived)::text = 'only' THEN archived_at IS NOT NULL ELSE archived_at IS NULL END)
  AND (sqlc.arg(scope)::text = '' OR (sqlc.arg(scope)::text = 'catalog' AND scope = 0)
    OR (sqlc.arg(scope)::text = 'event' AND scope = 1))
  AND (COALESCE(cardinality(sqlc.arg(event_ids)::uuid[]), 0) = 0 OR owner_event_id = ANY (sqlc.arg(event_ids)::uuid[])
    OR (scope = 0 AND EXISTS (SELECT 1
                              FROM unnest(sqlc.arg(event_ids)::uuid[]) AS selected(event_id)
                              WHERE exercise_available_to_event(exercises.id, selected.event_id))))
  AND (sqlc.arg(infrastructure)::text = ''
    OR exercise_has_infrastructure(id) = (sqlc.arg(infrastructure)::text = 'yes'))
  AND (sqlc.narg(viewer_id)::uuid IS NULL OR exercise_readable_by(id, sqlc.narg(viewer_id)::uuid));

-- name: ListExercisesPage :many
-- status is the single derived catalog status (exercise_status); the working
-- copy counts only for readers who may see it (admins, members of the owner
-- event) — for the others a catalog exercise is simply "published".
WITH listed AS (SELECT id,
                       exercise_status(draft_version_id, published_version_id, archived_at IS NOT NULL,
                                       sqlc.narg(viewer_id)::uuid IS NULL OR scope = 1)::text AS status
                FROM exercises
                WHERE (sqlc.arg(search)::text = '' OR name ILIKE '%' || sqlc.arg(search)::text || '%'
                    OR description ILIKE '%' || sqlc.arg(search)::text || '%')
                  AND (cardinality(sqlc.arg(tags)::text[]) = 0 OR tags && sqlc.arg(tags)::text[])
                  AND (CASE WHEN sqlc.arg(archived)::text = 'only' THEN archived_at IS NOT NULL ELSE archived_at IS NULL END)
                  AND (sqlc.arg(scope)::text = '' OR (sqlc.arg(scope)::text = 'catalog' AND scope = 0)
                    OR (sqlc.arg(scope)::text = 'event' AND scope = 1))
                  AND (COALESCE(cardinality(sqlc.arg(event_ids)::uuid[]), 0) = 0 OR owner_event_id = ANY (sqlc.arg(event_ids)::uuid[])
                    OR (scope = 0 AND EXISTS (SELECT 1
                                              FROM unnest(sqlc.arg(event_ids)::uuid[]) AS selected(event_id)
                                              WHERE exercise_available_to_event(exercises.id, selected.event_id))))
                  AND (sqlc.arg(infrastructure)::text = ''
                    OR exercise_has_infrastructure(id) = (sqlc.arg(infrastructure)::text = 'yes'))
                  AND (sqlc.narg(viewer_id)::uuid IS NULL OR exercise_readable_by(id, sqlc.narg(viewer_id)::uuid)))
SELECT sqlc.embed(exercises), listed.status
FROM listed
JOIN exercises USING (id)
WHERE (sqlc.arg(status)::text = '' OR listed.status = sqlc.arg(status)::text)
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'name' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(name) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'name' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(name) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'tags' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(array_to_string(tags, ',')) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'tags' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(array_to_string(tags, ',')) END DESC,
  -- Status rank: none < draft_only < changed < published < archived.
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'asc' THEN
    array_position(ARRAY ['none', 'draft_only', 'changed', 'published', 'archived'], listed.status) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'desc' THEN
    array_position(ARRAY ['none', 'draft_only', 'changed', 'published', 'archived'], listed.status) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'updated' AND sqlc.arg(sort_dir)::text = 'asc' THEN updated_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'updated' AND sqlc.arg(sort_dir)::text = 'desc' THEN updated_at END DESC,
  exercises.updated_at DESC, exercises.id DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountExercisesPage :one
WITH listed AS (SELECT exercise_status(draft_version_id, published_version_id, archived_at IS NOT NULL,
                                       sqlc.narg(viewer_id)::uuid IS NULL OR scope = 1)::text AS status
                FROM exercises
                WHERE (sqlc.arg(search)::text = '' OR name ILIKE '%' || sqlc.arg(search)::text || '%'
                    OR description ILIKE '%' || sqlc.arg(search)::text || '%')
                  AND (cardinality(sqlc.arg(tags)::text[]) = 0 OR tags && sqlc.arg(tags)::text[])
                  AND (CASE WHEN sqlc.arg(archived)::text = 'only' THEN archived_at IS NOT NULL ELSE archived_at IS NULL END)
                  AND (sqlc.arg(scope)::text = '' OR (sqlc.arg(scope)::text = 'catalog' AND scope = 0)
                    OR (sqlc.arg(scope)::text = 'event' AND scope = 1))
                  AND (COALESCE(cardinality(sqlc.arg(event_ids)::uuid[]), 0) = 0 OR owner_event_id = ANY (sqlc.arg(event_ids)::uuid[])
                    OR (scope = 0 AND EXISTS (SELECT 1
                                              FROM unnest(sqlc.arg(event_ids)::uuid[]) AS selected(event_id)
                                              WHERE exercise_available_to_event(exercises.id, selected.event_id))))
                  AND (sqlc.arg(infrastructure)::text = ''
                    OR exercise_has_infrastructure(id) = (sqlc.arg(infrastructure)::text = 'yes'))
                  AND (sqlc.narg(viewer_id)::uuid IS NULL OR exercise_readable_by(id, sqlc.narg(viewer_id)::uuid)))
SELECT count(*)
FROM listed
WHERE (sqlc.arg(status)::text = '' OR listed.status = sqlc.arg(status)::text);

-- name: ListExerciseTags :many
-- Tags of existing exercises by use; an empty prefix returns the most used.
-- Prefix matching is literal (%, _ are not LIKE wildcards) and
-- case-insensitive. viewer_id set = count only exercises the viewer may read.
SELECT tag::text AS tag, count(DISTINCT e.id)::bigint AS exercise_count
FROM exercises AS e
CROSS JOIN LATERAL unnest(e.tags) AS tag
WHERE (sqlc.arg(prefix)::text = ''
    OR left(tag, char_length(sqlc.arg(prefix)::text)) = lower(sqlc.arg(prefix)::text))
  AND (sqlc.narg(viewer_id)::uuid IS NULL OR exercise_readable_by(e.id, sqlc.narg(viewer_id)::uuid))
GROUP BY tag
ORDER BY exercise_count DESC, tag ASC
LIMIT sqlc.arg(limit_val);

-- name: ExerciseDraftDiffersFromPublished :one
-- jsonb equality is semantic (key order and whitespace are irrelevant), so a
-- working copy re-saved or restored with the published content compares
-- equal. Zero rows when either pointer is NULL: those cases are decided by
-- the domain (Exercise.HasUnpublishedChanges) without asking.
-- label is deliberately not compared: it names snapshots, never content.
SELECT (d.variants <> p.variants OR d.admin_note <> p.admin_note)::boolean AS differs
FROM exercises e
JOIN exercise_versions d ON d.id = e.draft_version_id
JOIN exercise_versions p ON p.id = e.published_version_id
WHERE e.id = sqlc.arg(exercise_id);

-- name: ListExerciseUsageEvents :many
-- Events that reference any version of the exercise through any attachment
-- revision (active or superseded), each once. Name is the platform-facing
-- internal name (falls back to the public one). Archived uses the same rule
-- as the events list: the archive instant has passed.
SELECT ev.id,
       COALESCE(NULLIF(ev.internal_name, ''), ev.name)::text AS name,
       (ev.archive_at IS NOT NULL AND ev.archive_at <= sqlc.arg(now)::timestamptz)::boolean AS archived
FROM events ev
WHERE EXISTS (SELECT 1
              FROM event_exercises ee
              WHERE ee.event_id = ev.id
                AND ee.exercise_id = sqlc.arg(exercise_id))
ORDER BY lower(COALESCE(NULLIF(ev.internal_name, ''), ev.name)), ev.id;

-- name: ListExercisesEventAccess :many
-- The "selected events" of a page of exercises in one query, with the
-- platform-facing event name.
SELECT access.exercise_id,
       access.event_id,
       COALESCE(NULLIF(ev.internal_name, ''), ev.name)::text AS event_name
FROM exercise_event_access access
JOIN events ev ON ev.id = access.event_id
WHERE access.exercise_id = ANY (sqlc.arg(exercise_ids)::uuid[])
ORDER BY access.exercise_id, lower(COALESCE(NULLIF(ev.internal_name, ''), ev.name)), ev.id;

-- name: DeleteExerciseEventAccess :exec
DELETE
FROM exercise_event_access
WHERE exercise_id = sqlc.arg(exercise_id);

-- name: InsertExerciseEventAccess :exec
INSERT INTO exercise_event_access (exercise_id, event_id)
SELECT sqlc.arg(exercise_id), unnest(sqlc.arg(event_ids)::uuid[])
ON CONFLICT DO NOTHING;

-- name: IsExerciseReadableBy :one
SELECT exercise_readable_by(sqlc.arg(exercise_id), sqlc.arg(viewer_id))::boolean;

-- name: IsExerciseAvailableToEvent :one
SELECT exercise_available_to_event(sqlc.arg(exercise_id), sqlc.arg(event_id))::boolean;

-- name: GetExerciseInfrastructure :one
SELECT exercise_has_infrastructure(sqlc.arg(exercise_id))::boolean;

-- name: ListExerciseCardExtras :many
-- Card decorations for a page of exercises in one query: owner event name,
-- fork source name, infrastructure and a pending proposal.
SELECT e.id,
       COALESCE(NULLIF(owner.internal_name, ''), owner.name, '')::text AS owner_event_name,
       COALESCE(source.name, '')::text AS forked_from_name,
       exercise_has_infrastructure(e.id)::boolean AS infrastructure,
       proposal.id AS pending_proposal_id
FROM exercises e
LEFT JOIN events owner ON owner.id = e.owner_event_id
LEFT JOIN exercises source ON source.id = e.forked_from_exercise_id
LEFT JOIN exercise_proposals proposal ON proposal.exercise_id = e.id AND proposal.status = 0
WHERE e.id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ListFileExerciseIDs :many
-- Exercises whose versions reference a file (attachment download policy).
SELECT DISTINCT version.exercise_id
FROM file_references reference
JOIN exercise_versions version ON version.id = reference.ref_id
WHERE reference.file_id = sqlc.arg(file_id)
  AND reference.ref_type = sqlc.arg(ref_type);

-- name: ListFileOwners :many
-- Who uploaded each of the files: an exercise draft may attach only files its author uploaded or the exercise
-- already holds.
SELECT id, created_by
FROM files
WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ListUserEventMemberships :many
-- The events a user is a member of, for the exercises app rights summary.
SELECT member.event_id, member.role,
       COALESCE(NULLIF(ev.internal_name, ''), ev.name)::text AS name,
       ev.tag, ev.infrastructure_allowed
FROM event_managers member
JOIN events ev ON ev.id = member.event_id
WHERE member.user_id = sqlc.arg(user_id)
ORDER BY lower(COALESCE(NULLIF(ev.internal_name, ''), ev.name)), ev.id;

-- name: ArchiveEventExercises :exec
-- Event deletion archives the exercises the event owns (same transaction as
-- the delete; the owner pointer is then nulled by the foreign key).
UPDATE exercises
SET archived_at = COALESCE(archived_at, sqlc.arg(now)::timestamptz),
    updated_at  = sqlc.arg(now)::timestamptz
WHERE owner_event_id = sqlc.arg(event_id);

-- name: CreateExerciseProposal :one
INSERT INTO exercise_proposals (id, exercise_id, event_id, status, note, proposed_by, proposed_at)
VALUES (sqlc.arg(id), sqlc.arg(exercise_id), sqlc.narg(event_id), 0, sqlc.arg(note), sqlc.narg(proposed_by), sqlc.arg(proposed_at))
RETURNING *;

-- name: GetExerciseProposal :one
SELECT *
FROM exercise_proposals
WHERE id = sqlc.arg(id);

-- name: DecideExerciseProposal :execrows
UPDATE exercise_proposals
SET status              = sqlc.arg(status),
    decided_by          = sqlc.narg(decided_by),
    decided_at          = sqlc.arg(decided_at),
    decision_note       = sqlc.arg(decision_note),
    catalog_exercise_id = sqlc.narg(catalog_exercise_id)
WHERE id = sqlc.arg(id)
  AND status = 0;

-- name: ListExerciseProposals :many
SELECT proposal.*,
       source.name::text AS exercise_name,
       COALESCE(NULLIF(ev.internal_name, ''), ev.name, '')::text AS event_name,
       COALESCE(NULLIF(btrim(concat_ws(' ', person.first_name, person.last_name)), ''), person.email, '')::text AS proposed_by_name
FROM exercise_proposals proposal
JOIN exercises source ON source.id = proposal.exercise_id
LEFT JOIN events ev ON ev.id = proposal.event_id
LEFT JOIN users person ON person.id = proposal.proposed_by
WHERE (sqlc.narg(status)::smallint IS NULL OR proposal.status = sqlc.narg(status)::smallint)
ORDER BY proposal.proposed_at DESC, proposal.id DESC
LIMIT 200;

-- name: GetEventInfrastructureAllowed :one
SELECT infrastructure_allowed
FROM events
WHERE id = sqlc.arg(id);
