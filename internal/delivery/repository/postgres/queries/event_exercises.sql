-- name: CreateEventExercise :one
INSERT INTO event_exercises (id, event_id, exercise_id, exercise_version_id, variant_mode, fixed_variant_index, revision, status,
                            replaces_event_exercise_id, created_at, created_by)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(exercise_id), sqlc.arg(exercise_version_id), sqlc.arg(variant_mode),
        sqlc.arg(fixed_variant_index), sqlc.arg(revision), sqlc.arg(status), sqlc.arg(replaces_event_exercise_id),
        sqlc.arg(created_at), sqlc.arg(created_by))
RETURNING *;

-- name: GetEventExerciseByID :one
SELECT *
FROM event_exercises
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: SupersedeEventExercise :execrows
UPDATE event_exercises
SET status = 1,
    superseded_at = sqlc.arg(superseded_at)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND status = 0;

-- name: ListEventExercises :many
SELECT *
FROM event_exercises
WHERE event_id = sqlc.arg(event_id)
ORDER BY created_at ASC, id ASC;

-- name: UpdateEventExerciseSource :one
-- In-place switch of the pinned source (update / fork / revert): the row and
-- its board challenges stay, the revision counts the switches.
UPDATE event_exercises
SET exercise_id         = sqlc.arg(exercise_id),
    exercise_version_id = sqlc.arg(exercise_version_id),
    revision            = revision + 1
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND status = 0
RETURNING *;

-- name: DetachEventExercise :execrows
UPDATE event_exercises
SET status      = 2,
    detached_at = sqlc.arg(detached_at),
    detached_by = sqlc.narg(detached_by)
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id)
  AND status = 0;

-- name: DeleteEventExercise :execrows
DELETE
FROM event_exercises
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: EventExerciseHasAttempts :one
SELECT EXISTS (SELECT 1
               FROM challenge_attempts attempt
               JOIN team_challenges tc ON tc.id = attempt.team_challenge_id
               JOIN event_challenges ec ON ec.id = tc.event_challenge_id
               WHERE ec.event_exercise_id = sqlc.arg(event_exercise_id))::boolean;

-- name: EventHasActiveExerciseFamily :one
-- Whether an active attachment of the event already uses the exercise, a fork
-- of it, or the source it was forked from.
SELECT EXISTS (SELECT 1
               FROM event_exercises ee
               JOIN exercises attached ON attached.id = ee.exercise_id
               JOIN exercises candidate ON candidate.id = sqlc.arg(exercise_id)
               WHERE ee.event_id = sqlc.arg(event_id)
                 AND ee.status = 0
                 AND (attached.id = candidate.id
                   OR attached.forked_from_exercise_id = candidate.id
                   OR candidate.forked_from_exercise_id = attached.id))::boolean;

-- name: FindEventFork :one
-- The event's own, active fork of a source exercise (reused by "fork").
SELECT *
FROM exercises
WHERE scope = 1
  AND owner_event_id = sqlc.arg(event_id)
  AND forked_from_exercise_id = sqlc.arg(source_exercise_id)
  AND archived_at IS NULL
  AND published_version_id IS NOT NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: ListEventExerciseDetails :many
-- The event's attachments with everything the manage list shows, in one
-- query: names, scope, catalog version numbers (ordinal among published
-- versions), newer versions, fork source, infrastructure, counts.
SELECT ee.id, ee.event_id, ee.exercise_id, ee.exercise_version_id, ee.variant_mode, ee.fixed_variant_index,
       ee.revision, ee.status, ee.replaces_event_exercise_id, ee.superseded_at, ee.detached_at, ee.created_at, ee.stage_id,
       pinned.name::text AS exercise_name,
       ex.scope,
       -- The pinned version's categories (catalog tags), for the event's task statistics.
       COALESCE(pinned.tags, '{}')::text[] AS exercise_tags,
       ex.published_version_id AS latest_version_id,
       ex.forked_from_exercise_id,
       ex.forked_from_version_id,
       (SELECT count(*) FROM exercise_versions v
        WHERE v.exercise_id = ee.exercise_id AND v.published_at IS NOT NULL
          AND v.published_at <= COALESCE(pinned.published_at, 'infinity'::timestamptz))::integer AS version_number,
       (SELECT count(*) FROM exercise_versions v
        WHERE v.exercise_id = ee.exercise_id AND v.published_at IS NOT NULL)::integer AS latest_version_number,
       COALESCE(source_pinned.name, source.name, '')::text AS source_name,
       source.published_version_id AS source_latest_version_id,
       (SELECT count(*) FROM exercise_versions v
        WHERE v.exercise_id = ex.forked_from_exercise_id AND v.published_at IS NOT NULL
          AND v.published_at <= COALESCE(source_pinned.published_at, 'infinity'::timestamptz))::integer AS source_version_number,
       (SELECT count(*) FROM exercise_versions v
        WHERE v.exercise_id = ex.forked_from_exercise_id AND v.published_at IS NOT NULL)::integer AS source_latest_version_number,
       exercise_variants_have_infrastructure(pinned.variants)::boolean AS infrastructure,
       CASE WHEN jsonb_typeof(pinned.variants) = 'array' THEN jsonb_array_length(pinned.variants) ELSE 0 END::integer AS variant_count,
       -- The PINNED version, as the catalog shows a version: the average difficulty (NULL scans badly, hence the
       -- flag), whether there is a variant, and the subtask names in order, of the fixed variant of a fixed
       -- attachment, else of the first one (every variant has the same subtasks at the same levels).
       (pinned.avg_difficulty IS NOT NULL)::boolean AS has_avg_difficulty,
       COALESCE(pinned.avg_difficulty, 0)::float8 AS avg_difficulty,
       (jsonb_typeof(pinned.variants -> 0) = 'object')::boolean AS has_task_count,
       COALESCE(ARRAY(SELECT COALESCE(task.value ->> 'name', '')
                      FROM jsonb_array_elements(CASE
                                                    WHEN jsonb_typeof(pinned.variants -> CASE WHEN ee.variant_mode = 1 THEN COALESCE(ee.fixed_variant_index, 0) ELSE 0 END -> 'tasks') = 'array'
                                                        THEN pinned.variants -> CASE WHEN ee.variant_mode = 1 THEN COALESCE(ee.fixed_variant_index, 0) ELSE 0 END -> 'tasks'
                                                    ELSE '[]'::jsonb END) WITH ORDINALITY AS task(value, position)
                      ORDER BY task.position), '{}')::text[] AS task_names,
       (SELECT count(*) FROM event_challenges ec WHERE ec.event_exercise_id = ee.id)::integer AS challenge_count,
       (SELECT count(*) FROM event_challenges ec WHERE ec.event_exercise_id = ee.id AND ec.published)::integer AS published_count,
       EXISTS (SELECT 1
               FROM challenge_attempts attempt
               JOIN team_challenges tc ON tc.id = attempt.team_challenge_id
               JOIN event_challenges ec ON ec.id = tc.event_challenge_id
               WHERE ec.event_exercise_id = ee.id)::boolean AS has_attempts
FROM event_exercises ee
JOIN exercises ex ON ex.id = ee.exercise_id
JOIN exercise_versions pinned ON pinned.id = ee.exercise_version_id
LEFT JOIN exercises source ON source.id = ex.forked_from_exercise_id
LEFT JOIN exercise_versions source_pinned ON source_pinned.id = ex.forked_from_version_id
WHERE ee.event_id = sqlc.arg(event_id)
ORDER BY ee.created_at ASC, ee.id ASC;

-- name: CountEventInfrastructureExercises :one
-- Active attachments whose pinned set has a lab topology: they block turning
-- the event's infrastructure off.
SELECT count(*)::integer
FROM event_exercises ee
JOIN exercise_versions pinned ON pinned.id = ee.exercise_version_id
WHERE ee.event_id = sqlc.arg(event_id)
  AND ee.status = 0
  AND exercise_variants_have_infrastructure(pinned.variants);

-- name: ListEventCatalog :many
-- Published exercises an event may attach: its own and the catalog entries
-- available to it. attached: the event already uses it or its fork family.
SELECT ex.id, published.name, published.description, published.tags, ex.scope, ex.published_version_id::uuid AS published_version_id,
       exercise_has_infrastructure(ex.id)::boolean AS infrastructure,
       -- The published version's first variant, as the catalog table shows it: the average difficulty (NULL scans
       -- badly, hence the flag), the subtask count (NULL without a variant) and the subtask names in order.
       (published.avg_difficulty IS NOT NULL)::boolean AS has_avg_difficulty,
       COALESCE(published.avg_difficulty, 0)::float8 AS avg_difficulty,
       (jsonb_typeof(published.variants -> 0) = 'object')::boolean AS has_task_count,
       COALESCE(ARRAY(SELECT COALESCE(task.value ->> 'name', '')
                      FROM jsonb_array_elements(CASE
                                                    WHEN jsonb_typeof(published.variants -> 0 -> 'tasks') = 'array'
                                                        THEN published.variants -> 0 -> 'tasks'
                                                    ELSE '[]'::jsonb END) WITH ORDINALITY AS task(value, position)
                      ORDER BY task.position), '{}')::text[] AS task_names,
       EXISTS (SELECT 1
               FROM event_exercises ee
               JOIN exercises attached ON attached.id = ee.exercise_id
               WHERE ee.event_id = sqlc.arg(event_id)
                 AND ee.status = 0
                 AND (attached.id = ex.id OR attached.forked_from_exercise_id = ex.id
                   OR ex.forked_from_exercise_id = attached.id))::boolean AS attached,
       -- How many exercises match the filters before the LIMIT, and how many the event may attach at all.
       (count(*) OVER ())::integer AS matched_count,
       (SELECT count(*)
        FROM exercises avail
        WHERE avail.published_version_id IS NOT NULL
          AND avail.archived_at IS NULL
          AND exercise_available_to_event(avail.id, sqlc.arg(event_id)))::integer AS available_count
FROM exercises ex
JOIN exercise_versions published ON published.id = ex.published_version_id
WHERE ex.published_version_id IS NOT NULL
  AND ex.archived_at IS NULL
  AND exercise_available_to_event(ex.id, sqlc.arg(event_id))
  AND (sqlc.arg(search)::text = '' OR published.name ILIKE '%' || sqlc.arg(search)::text || '%'
    OR published.description ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(infrastructure)::text = ''
    OR exercise_has_infrastructure(ex.id) = (sqlc.arg(infrastructure)::text = 'yes'))
  AND (coalesce(cardinality(sqlc.arg(tags)::text[]), 0) = 0 OR published.tags && sqlc.arg(tags)::text[])
  -- difficulties: the rounded average level 1..6 (as the catalog filter rounds it), 0 for a set without one; any of
  -- them (union); empty means no difficulty filter.
  AND (coalesce(cardinality(sqlc.arg(difficulties)::int[]), 0) = 0
    OR COALESCE(round(published.avg_difficulty)::int, 0) = ANY (sqlc.arg(difficulties)::int[]))
  -- attached: yes or no keeps only the exercises the event already uses (or not); '' keeps both.
  AND (sqlc.arg(attached)::text = ''
    OR EXISTS (SELECT 1
               FROM event_exercises ee
               JOIN exercises attached ON attached.id = ee.exercise_id
               WHERE ee.event_id = sqlc.arg(event_id)
                 AND ee.status = 0
                 AND (attached.id = ex.id OR attached.forked_from_exercise_id = ex.id
                   OR ex.forked_from_exercise_id = attached.id)) = (sqlc.arg(attached)::text = 'yes'))
ORDER BY ex.scope DESC, lower(published.name), ex.id
LIMIT 100;

-- name: ListEventCatalogTags :many
-- Tags of the exercises ListEventCatalog offers this event (published,
-- non-archived, available to it), most used first. An empty prefix returns
-- the most used. Prefix matching is literal (%, _ are not LIKE wildcards) and
-- case-insensitive.
SELECT tag::text AS tag, count(DISTINCT ex.id)::bigint AS exercise_count
FROM exercises AS ex
JOIN exercise_versions published ON published.id = ex.published_version_id
CROSS JOIN LATERAL unnest(published.tags) AS tag
WHERE ex.published_version_id IS NOT NULL
  AND ex.archived_at IS NULL
  AND exercise_available_to_event(ex.id, sqlc.arg(event_id))
  AND (sqlc.arg(prefix)::text = ''
    OR left(tag, char_length(sqlc.arg(prefix)::text)) = lower(sqlc.arg(prefix)::text))
GROUP BY tag
ORDER BY exercise_count DESC, tag ASC
LIMIT sqlc.arg(limit_val);
