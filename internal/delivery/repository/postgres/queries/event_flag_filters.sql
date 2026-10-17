-- name: GetEventFlagFilter :one
SELECT * FROM event_flag_filters
WHERE event_exercise_id = $1 AND variant_index = $2 AND task_id = $3;

-- name: UpsertEventFlagFilter :exec
-- The filter row is created once and then rewritten in place.
INSERT INTO event_flag_filters (event_exercise_id, variant_index, task_id, fingerprint, mode, salt, hashes, size, bits, inserted, capacity, resets)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (event_exercise_id, variant_index, task_id) DO UPDATE
SET fingerprint = excluded.fingerprint, mode = excluded.mode, salt = excluded.salt, hashes = excluded.hashes, size = excluded.size, bits = excluded.bits,
    inserted = excluded.inserted, capacity = excluded.capacity, resets = excluded.resets;

-- name: ListEventFlagFilters :many
-- The filter rows of an event with their reset counts (a reset filter may
-- repeat flags; a rebuild keeps the count).
SELECT f.event_exercise_id, f.variant_index, f.task_id, f.resets
FROM event_flag_filters f
JOIN event_exercises ee ON ee.id = f.event_exercise_id
WHERE ee.event_id = sqlc.arg(event_id);

-- name: ListTaskVariantIssuedFlags :many
-- The stored expected flags of one task variant of an event exercise.
SELECT tc.expected_flag
FROM team_challenges tc
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
WHERE ec.event_exercise_id = sqlc.arg(event_exercise_id)
  AND ec.task_id = sqlc.arg(task_id)
  AND tc.variant_index = sqlc.arg(variant_index);

-- name: ListEventIssuedFlags :many
-- Every stored expected flag of an event by (event exercise, variant, task),
-- one pass for a rebuild of the event's filters.
SELECT ec.event_exercise_id, tc.variant_index, ec.task_id, tc.expected_flag
FROM team_challenges tc
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
WHERE tc.event_id = sqlc.arg(event_id);

-- name: ListVersionTaskFlags :many
-- The flag candidates of every task of pinned versions, by variant position.
SELECT version.id AS version_id,
       COALESCE((SELECT jsonb_agg(jsonb_build_object(
                           'tasks', COALESCE((SELECT jsonb_agg(jsonb_build_object('id', task -> 'id', 'flag', task -> 'flag'))
                                              FROM jsonb_array_elements(CASE WHEN jsonb_typeof(variant -> 'tasks') = 'array' THEN variant -> 'tasks' ELSE '[]'::jsonb END) AS task),
                                             '[]'::jsonb)) ORDER BY position)
                 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(version.variants) = 'array' THEN version.variants ELSE '[]'::jsonb END) WITH ORDINALITY AS v(variant, position)),
                '[]'::jsonb)::jsonb AS variants
FROM exercise_versions version
WHERE version.id = ANY (sqlc.arg(ids)::uuid[]);
