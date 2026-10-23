-- name: CreateExerciseCheckpoint :one
-- A deliberate immutable copy of the working copy: the draft row or, while
-- the working copy is not materialized yet, the published version (at most
-- one of each per exercise; the draft wins). The label names the snapshot
-- only; content (admin_note, variants) is copied untouched. Zero rows:
-- nothing to snapshot.
INSERT INTO exercise_versions (id, exercise_id, status, admin_note, label, variants, created_at, created_by, name, description, tags)
SELECT sqlc.arg(new_id), src.exercise_id, 'checkpoint', src.admin_note, sqlc.arg(label)::text,
       src.variants, sqlc.arg(created_at), sqlc.arg(created_by), src.name, src.description, src.tags
FROM exercise_versions src
WHERE src.exercise_id = sqlc.arg(exercise_id)
  AND (src.status = 'draft'
    OR (src.status = 'published'
        AND NOT EXISTS (SELECT 1
                        FROM exercise_versions dv
                        WHERE dv.exercise_id = sqlc.arg(exercise_id)
                          AND dv.status = 'draft')))
RETURNING *;

-- name: RestoreVersionPreservingDraft :one
-- Save the current draft as a checkpoint in the same statement before
-- replacing its contents with the selected historical version. The source
-- must belong to this exercise; a foreign id produces zero rows.
WITH ex AS (SELECT exercises.id FROM exercises WHERE exercises.id = sqlc.arg(exercise_id) FOR UPDATE), source AS (
    SELECT admin_note, variants, name, description, tags
    FROM exercise_versions src
    WHERE src.id = sqlc.arg(source_id)
      AND src.exercise_id = (SELECT id FROM ex)
      AND src.status <> 'draft'
), locked_draft AS MATERIALIZED (
    -- A parent lock acquired after waiting does not refresh this statement's
    -- snapshot. Lock the draft too: READ COMMITTED rechecks an updated row and
    -- returns its current full contents, including the just-acknowledged edit.
    SELECT d.*
    FROM exercise_versions d JOIN ex ON ex.id = d.exercise_id
    WHERE d.status = 'draft' AND EXISTS (SELECT 1 FROM source)
    FOR UPDATE OF d
), preserved AS (
    INSERT INTO exercise_versions (id, exercise_id, status, admin_note, variants, created_at, created_by, name, description, tags)
    SELECT sqlc.arg(checkpoint_id), d.exercise_id, 'checkpoint', d.admin_note, d.variants,
           sqlc.arg(created_at), sqlc.arg(created_by), d.name, d.description, d.tags
    FROM locked_draft d
    RETURNING id
), restored AS (
    UPDATE exercise_versions d
    SET admin_note = source.admin_note,
        variants = source.variants,
        name = source.name, description = source.description, tags = source.tags
    FROM source
    WHERE d.exercise_id = sqlc.arg(exercise_id)
      AND d.status = 'draft'
      AND EXISTS (SELECT 1 FROM preserved)
    RETURNING d.*
)
SELECT * FROM restored;
