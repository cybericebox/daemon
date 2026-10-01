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
INSERT INTO exercise_versions (id, exercise_id, status, admin_note, label, variants, created_at, created_by, published_at)
VALUES (sqlc.arg(id), sqlc.arg(exercise_id), sqlc.arg(status), sqlc.arg(admin_note), sqlc.arg(label), sqlc.arg(variants), sqlc.arg(created_at), sqlc.arg(created_by), sqlc.arg(published_at))
RETURNING *;

-- name: SetImportedExercisePointers :exec
UPDATE exercises SET draft_version_id = sqlc.narg(draft_version_id), published_version_id = sqlc.narg(published_version_id)
WHERE id = sqlc.arg(id);

-- name: UpsertExerciseDraft :one
-- Draft save is an upsert against the partial-unique draft slot: update the
-- existing draft's content or insert a fresh draft row, then point
-- exercises.draft_version_id at it. Anchored on the exercises row: a missing
-- exercise yields zero rows (caller maps to 404). Identity columns
-- (updated_at & co) are deliberately untouched — the identity optimistic lock
-- must not be disturbed by content saves.
WITH ex AS (SELECT id
            FROM exercises
            WHERE exercises.id = sqlc.arg(exercise_id)),
     updated AS (
         UPDATE exercise_versions v
             SET variants = sqlc.arg(variants),
                 admin_note = sqlc.arg(admin_note)
             WHERE v.exercise_id = (SELECT id FROM ex) AND v.status = 'draft'
             RETURNING v.*),
     inserted AS (
         INSERT INTO exercise_versions (id, exercise_id, status, admin_note, variants, created_at, created_by)
             SELECT sqlc.arg(new_id), ex.id, 'draft', sqlc.arg(admin_note), sqlc.arg(variants),
                    sqlc.arg(created_at), sqlc.arg(created_by)
             FROM ex
             WHERE NOT EXISTS (SELECT 1 FROM updated)
             RETURNING *),
     pointer AS (
         UPDATE exercises
             SET draft_version_id = (SELECT id FROM inserted)
             WHERE exercises.id = (SELECT id FROM ex) AND EXISTS (SELECT 1 FROM inserted))
SELECT * FROM updated
UNION ALL
SELECT * FROM inserted;

-- name: PublishExerciseDraft :one
-- Atomic family transition: demote the current published row, promote the
-- draft, move both pointers. Zero rows -> no draft (or no exercise); the
-- caller re-reads to discriminate.
WITH ex AS (SELECT id, draft_version_id, published_version_id
            FROM exercises
            WHERE exercises.id = sqlc.arg(exercise_id)
              AND draft_version_id IS NOT NULL),
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
                 draft_version_id = NULL
             WHERE exercises.id = (SELECT ex.id FROM ex)
                 AND EXISTS (SELECT 1 FROM promoted))
SELECT * FROM promoted;

-- name: DiscardExerciseDraft :one
-- exercises.draft_version_id clears itself via ON DELETE SET NULL.
DELETE
FROM exercise_versions
WHERE exercise_id = $1
  AND status = 'draft'
RETURNING id;

-- name: CreateDraftFromVersion :one
-- Rollback: a new draft cloned from a historical version's content. Fails
-- with zero rows when the source is missing OR a draft already exists; the
-- caller re-reads to discriminate 404 vs 409.
WITH src AS (SELECT *
             FROM exercise_versions
             WHERE exercise_versions.id = sqlc.arg(source_id)
               AND exercise_versions.exercise_id = sqlc.arg(exercise_id)),
     inserted AS (
         INSERT INTO exercise_versions (id, exercise_id, status, admin_note, variants, created_at, created_by)
             SELECT sqlc.arg(new_id), src.exercise_id, 'draft', src.admin_note, src.variants,
                    sqlc.arg(created_at), sqlc.arg(created_by)
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
