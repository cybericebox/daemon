-- Background imports of task archives with device images (the import job aggregate: one row per source).

-- name: StartExerciseImportJob :one
-- Records a new import of the source, or returns the row the source already has: one import per source.
INSERT INTO exercise_import_jobs (id, created_by, source, source_id, state, exercise_ids, error, created_at, updated_at,
                                  owner_event_id, options, plan_hash)
VALUES (sqlc.arg(id), sqlc.arg(created_by), sqlc.arg(source), sqlc.arg(source_id), sqlc.arg(state),
        sqlc.arg(exercise_ids)::uuid[], sqlc.narg(error), sqlc.arg(created_at), sqlc.arg(updated_at),
        sqlc.narg(owner_event_id), COALESCE(sqlc.narg(options)::jsonb, '{}'::jsonb), sqlc.arg(plan_hash))
ON CONFLICT (source_id) DO UPDATE SET source_id = EXCLUDED.source_id
RETURNING *;

-- name: GetExerciseImportJob :one
SELECT *
FROM exercise_import_jobs
WHERE id = sqlc.arg(id);

-- name: UpdateExerciseImportJob :execrows
-- The outcome belongs to one running attempt; a stale worker cannot overwrite a newer attempt or done state.
UPDATE exercise_import_jobs
SET state          = sqlc.arg(state),
    owner_event_id = sqlc.narg(owner_event_id),
    exercise_ids = sqlc.arg(exercise_ids)::uuid[],
    error        = sqlc.narg(error),
    stage        = sqlc.arg(stage),
    images_done  = sqlc.arg(images_done),
    images_total = sqlc.arg(images_total),
    tasks_done   = sqlc.arg(tasks_done),
    tasks_total  = sqlc.arg(tasks_total),
    bytes_pushed = sqlc.arg(bytes_pushed),
    bytes_total  = sqlc.arg(bytes_total),
    results      = sqlc.arg(results),
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id) AND generation = sqlc.arg(generation) AND state = 'running';

-- name: UpdateExerciseImportJobProgress :execrows
-- The throttled progress write of a running import: a narrow write, so it never races the outcome write; a job that
-- has ended or restarted (zero rows) keeps its outcome.
UPDATE exercise_import_jobs
SET stage        = sqlc.arg(stage),
    images_done  = sqlc.arg(images_done),
    images_total = sqlc.arg(images_total),
    tasks_done   = sqlc.arg(tasks_done),
    tasks_total  = sqlc.arg(tasks_total),
    bytes_pushed = sqlc.arg(bytes_pushed),
    bytes_total  = sqlc.arg(bytes_total),
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id)
  AND state = 'running' AND generation = sqlc.arg(generation);

-- name: ListOpenExerciseImportJobs :many
-- The imports of the user that are running or failed and not closed by the user (a running one silent since
-- stale_before has stopped: the caller reads it as failed).
SELECT j.*
FROM exercise_import_jobs j
WHERE j.created_by = sqlc.arg(created_by)
  AND j.state IN ('running', 'failed')
  AND NOT EXISTS (SELECT 1
                  FROM exercise_import_dismissals d
                  WHERE d.job_id = j.id AND d.dismissed_at >= j.updated_at)
ORDER BY j.created_at;

-- name: DismissExerciseImportJob :execrows
-- Only the user who started the import closes its row: a foreign job inserts nothing (zero rows).
INSERT INTO exercise_import_dismissals (job_id, dismissed_at)
SELECT j.id, sqlc.arg(dismissed_at)
FROM exercise_import_jobs j
WHERE j.id = sqlc.arg(job_id) AND j.created_by = sqlc.arg(created_by)
ON CONFLICT (job_id) DO UPDATE SET dismissed_at = EXCLUDED.dismissed_at;

-- name: GetOwnedExerciseImportJobBySource :one
-- Read-only recovery before source re-open. A foreign job is indistinguishable
-- from an absent job; no source INSERT/UPDATE is used to probe for it.
SELECT * FROM exercise_import_jobs
WHERE source_id = sqlc.arg(source_id) AND source = sqlc.arg(source)
  AND created_by = sqlc.arg(created_by);
