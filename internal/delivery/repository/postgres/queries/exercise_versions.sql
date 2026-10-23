-- name: GetDraftVersion :one
SELECT *
FROM exercise_versions
WHERE exercise_id = $1
  AND status = 'draft';

-- name: GetExerciseVersionByID :one
SELECT *
FROM exercise_versions
WHERE id = $1;

-- name: ListExerciseVersions :many
SELECT *
FROM exercise_versions
WHERE exercise_id = $1
ORDER BY created_at DESC, id DESC;

-- name: ListExerciseVersionIDs :many
SELECT id
FROM exercise_versions
WHERE exercise_id = $1;

-- name: InsertImportedExerciseVersion :one
INSERT INTO exercise_versions (id, exercise_id, status, admin_note, label, variants, created_at, created_by, published_at, name, description, tags)
VALUES (sqlc.arg(id), sqlc.arg(exercise_id), sqlc.arg(status), sqlc.arg(admin_note), sqlc.arg(label), sqlc.arg(variants), sqlc.arg(created_at), sqlc.arg(created_by), sqlc.arg(published_at), sqlc.arg(name), sqlc.arg(description), sqlc.arg(tags))
RETURNING *;

-- name: SetImportedExercisePointers :exec
UPDATE exercises e SET draft_version_id = sqlc.narg(draft_version_id)::uuid, published_version_id = sqlc.narg(published_version_id)::uuid,
    name = COALESCE((SELECT v.name FROM exercise_versions v WHERE v.id = COALESCE(sqlc.narg(published_version_id)::uuid, sqlc.narg(draft_version_id)::uuid)), e.name),
    description = COALESCE((SELECT v.description FROM exercise_versions v WHERE v.id = COALESCE(sqlc.narg(published_version_id)::uuid, sqlc.narg(draft_version_id)::uuid)), e.description),
    tags = COALESCE((SELECT v.tags FROM exercise_versions v WHERE v.id = COALESCE(sqlc.narg(published_version_id)::uuid, sqlc.narg(draft_version_id)::uuid)), e.tags)
WHERE e.id = sqlc.arg(id);

-- name: UpsertExerciseDraft :one
-- Serialize family writers on the identity first, then use PostgreSQL's
-- partial-unique conflict target. ON CONFLICT sees a concurrent first draft
-- even when it committed after this statement's read snapshot was taken.
WITH ex AS (SELECT * FROM exercises WHERE exercises.id = sqlc.arg(exercise_id) FOR UPDATE),
     saved AS (
         INSERT INTO exercise_versions (id, exercise_id, status, admin_note, variants, created_at, created_by, name, description, tags)
         SELECT sqlc.arg(new_id), ex.id, 'draft', sqlc.arg(admin_note), sqlc.arg(variants),
                sqlc.arg(created_at), sqlc.arg(created_by),
                COALESCE(p.name, ex.name), COALESCE(p.description, ex.description), COALESCE(p.tags, ex.tags)
         FROM ex LEFT JOIN exercise_versions p ON p.id = ex.published_version_id
         ON CONFLICT (exercise_id) WHERE status = 'draft' DO UPDATE
             SET variants = EXCLUDED.variants, admin_note = EXCLUDED.admin_note
         RETURNING *
     ), pointer AS (
         UPDATE exercises SET draft_version_id = (SELECT id FROM saved)
         WHERE exercises.id = (SELECT id FROM ex) AND EXISTS (SELECT 1 FROM saved)
     )
SELECT * FROM saved;

-- name: SaveExerciseDraftIdentity :one
-- The legacy PATCH edits metadata in the draft. A lazy draft inherits the
-- published content and file references; a following content PUT preserves
-- the edited metadata, including across a concurrent first-draft insert.
WITH ex AS (
    SELECT * FROM exercises
    WHERE exercises.id = sqlc.arg(exercise_id)
      AND exercises.updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at)
    FOR UPDATE
), saved AS (
    INSERT INTO exercise_versions (id, exercise_id, status, admin_note, variants, created_at, created_by, name, description, tags)
    SELECT sqlc.arg(new_id), ex.id, 'draft', COALESCE(p.admin_note, ''), COALESCE(p.variants, '[]'::jsonb),
           sqlc.arg(updated_at), sqlc.arg(updated_by), sqlc.arg(name), sqlc.arg(description), sqlc.arg(tags)
    FROM ex LEFT JOIN exercise_versions p ON p.id = ex.published_version_id
    ON CONFLICT (exercise_id) WHERE status = 'draft' DO UPDATE
        SET name = EXCLUDED.name, description = EXCLUDED.description, tags = EXCLUDED.tags
    RETURNING *
), copied_refs AS (
    INSERT INTO file_references (ref_type, ref_id, file_id)
    SELECT 'exercise_version', saved.id, refs.file_id
    FROM saved JOIN ex ON ex.id = saved.exercise_id
    JOIN file_references refs ON refs.ref_type = 'exercise_version' AND refs.ref_id = ex.published_version_id
    WHERE saved.id = sqlc.arg(new_id)
    ON CONFLICT DO NOTHING
), pointers AS (
    UPDATE exercises
    SET draft_version_id = (SELECT id FROM saved),
        updated_at = sqlc.arg(updated_at), updated_by = sqlc.arg(updated_by)
    WHERE exercises.id = (SELECT id FROM ex) AND EXISTS (SELECT 1 FROM saved)
)
SELECT * FROM saved;

-- name: PublishExerciseDraft :one
-- Atomic family transition: demote the current published row, promote the
-- draft, move both pointers. Zero rows -> no draft (or no exercise); the
-- caller re-reads to discriminate.
WITH ex AS (SELECT id, draft_version_id, published_version_id
            FROM exercises
            WHERE exercises.id = sqlc.arg(exercise_id)
              AND draft_version_id IS NOT NULL FOR UPDATE),
     demoted AS (
         UPDATE exercise_versions
             SET status = 'unpublished'
             WHERE exercise_versions.id = (SELECT published_version_id FROM ex)
                 AND status = 'published'
             RETURNING exercise_versions.id),
     promoted AS (
         UPDATE exercise_versions
             SET status = 'published',
                 published_at = sqlc.arg(published_at)
             WHERE exercise_versions.id = (SELECT draft_version_id FROM ex)
                 AND status = 'draft'
                 -- Forces Postgres to execute `demoted` to completion (including
                 -- its immediate unique-index check) before this UPDATE runs.
                 -- Without this data dependency the two writable CTEs have no
                 -- ordering guarantee and can both touch the partial unique
                 -- index "one published per exercise" in the same instant,
                 -- raising a spurious 23505 even though the end state is valid.
                 AND (SELECT count(*) FROM demoted) IS NOT NULL
             RETURNING exercise_versions.*),
     pointers AS (
         UPDATE exercises
             SET published_version_id = (SELECT promoted.id FROM promoted),
                 draft_version_id = NULL,
                 name = (SELECT promoted.name FROM promoted),
                 description = (SELECT promoted.description FROM promoted),
                 tags = (SELECT promoted.tags FROM promoted)
             WHERE exercises.id = (SELECT ex.id FROM ex)
                 AND EXISTS (SELECT 1 FROM promoted))
SELECT * FROM promoted;

-- name: DiscardExerciseDraft :one
-- exercises.draft_version_id clears itself via ON DELETE SET NULL.
WITH ex AS (SELECT exercises.id FROM exercises WHERE exercises.id = $1 FOR UPDATE)
DELETE
FROM exercise_versions
WHERE exercise_id = (SELECT id FROM ex)
  AND status = 'draft'
RETURNING id;

-- name: CreateDraftFromVersion :one
-- Rollback: a new draft cloned from a historical version's content. Fails
-- with zero rows when the source is missing OR a draft already exists; the
-- caller re-reads to discriminate 404 vs 409.
WITH ex AS (SELECT exercises.id FROM exercises WHERE exercises.id = sqlc.arg(exercise_id) FOR UPDATE),
     src AS (SELECT *
             FROM exercise_versions
             WHERE exercise_versions.id = sqlc.arg(source_id)
               AND exercise_versions.exercise_id = (SELECT id FROM ex)),
     inserted AS (
         INSERT INTO exercise_versions (id, exercise_id, status, admin_note, variants, created_at, created_by, name, description, tags)
             SELECT sqlc.arg(new_id), src.exercise_id, 'draft', src.admin_note, src.variants,
                    sqlc.arg(created_at), sqlc.arg(created_by), src.name, src.description, src.tags
             FROM src
             WHERE NOT EXISTS (SELECT 1
                               FROM exercise_versions dv
                               WHERE dv.exercise_id = sqlc.arg(exercise_id)
                                 AND dv.status = 'draft')
             RETURNING *),
     pointer AS (
         UPDATE exercises
             SET draft_version_id = (SELECT inserted.id FROM inserted)
             WHERE exercises.id = sqlc.arg(exercise_id) AND EXISTS (SELECT 1 FROM inserted))
SELECT * FROM inserted;
