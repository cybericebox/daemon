-- name: LockExerciseForImport :one
-- A separate statement before fresh reads: waiters see the latest acknowledged
-- family/policy snapshot, rather than the snapshot from before the lock wait.
SELECT id FROM exercises WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockExerciseImportJob :one
SELECT * FROM exercise_import_jobs WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: RestartExerciseImportJob :one
-- Only the observed failed/stale attempt may be replaced. Old queued arguments
-- normalize generation zero to the migrated generation one in the worker.
UPDATE exercise_import_jobs
SET generation = generation + 1,
    owner_event_id = sqlc.narg(owner_event_id), options = sqlc.arg(options), plan_hash = sqlc.arg(plan_hash),
    state = 'running', exercise_ids = '{}', results = '[]', error = NULL,
    stage = 'unpacking', images_done = 0, images_total = 0, tasks_done = 0, tasks_total = 0,
    bytes_pushed = 0, bytes_total = 0, updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(expected_generation)
  AND (state = 'failed' OR (state = 'running' AND updated_at < sqlc.arg(stale_before)))
RETURNING *;

-- name: TryCreateImportedExercise :one
-- Timestamps come from the domain factory, not DB defaults.
INSERT INTO exercises (id, name, description, tags, created_at, created_by, updated_at, updated_by,
                       scope, owner_event_id, access_level, origin_event_id, forked_from_exercise_id, forked_from_version_id)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(description), sqlc.arg(tags), sqlc.arg(created_at), sqlc.arg(created_by),
        sqlc.arg(updated_at), sqlc.arg(updated_by), sqlc.arg(scope), sqlc.narg(owner_event_id), sqlc.arg(access_level),
        sqlc.narg(origin_event_id), sqlc.narg(forked_from_exercise_id), sqlc.narg(forked_from_version_id))
ON CONFLICT DO NOTHING
RETURNING *;


-- name: PublishValidatedExerciseDraft :one
-- Atomic family transition: demote the current published row, promote the
-- draft, move both pointers. Zero rows -> no draft (or no exercise); the
-- caller re-reads to discriminate.
WITH ex AS (SELECT id, draft_version_id, published_version_id
            FROM exercises
            WHERE exercises.id = sqlc.arg(exercise_id)
              AND draft_version_id IS NOT NULL FOR UPDATE),
     validated AS MATERIALIZED (
         SELECT d.id FROM exercise_versions d JOIN ex ON d.id = ex.draft_version_id
         WHERE d.status = 'draft' AND d.id = sqlc.arg(expected_id)
           AND d.name = sqlc.arg(expected_name) AND d.description = sqlc.arg(expected_description)
           AND d.tags = sqlc.arg(expected_tags)::text[] AND d.admin_note = sqlc.arg(expected_admin_note)
           AND d.variants = sqlc.arg(expected_variants)::jsonb
         FOR UPDATE OF d
     ),
     demoted AS (
         UPDATE exercise_versions
             SET status = 'unpublished'
             WHERE exercise_versions.id = (SELECT published_version_id FROM ex)
                 AND status = 'published' AND EXISTS (SELECT 1 FROM validated)
             RETURNING exercise_versions.id),
     promoted AS (
         UPDATE exercise_versions
             SET status = 'published',
                 published_at = sqlc.arg(published_at)
             WHERE exercise_versions.id = (SELECT id FROM validated)
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


-- name: LockExerciseImportUpload :one
-- Stabilize source eligibility through the task/outcome transaction; bytes were
-- already staged outside it. Expiry is checked against wall time after waiting.
SELECT * FROM media_uploads WHERE id = sqlc.arg(id) FOR UPDATE;

-- name: LockExerciseImportFile :one
SELECT * FROM files WHERE id = sqlc.arg(id) FOR UPDATE;
