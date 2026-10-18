-- Background imports of task archives with device images (the import job aggregate: one row per source).

-- name: StartExerciseImportJob :one
-- Records a new import of the source, or returns the row the source already has: one import per source.
INSERT INTO exercise_import_jobs (id, created_by, source, source_id, state, exercise_ids, error, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(created_by), sqlc.arg(source), sqlc.arg(source_id), sqlc.arg(state),
        sqlc.arg(exercise_ids)::uuid[], sqlc.narg(error), sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT (source_id) DO UPDATE SET source_id = EXCLUDED.source_id
RETURNING *;

-- name: GetExerciseImportJob :one
SELECT *
FROM exercise_import_jobs
WHERE id = sqlc.arg(id);

-- name: UpdateExerciseImportJob :execrows
-- The whole job aggregate (state, created tasks, error, progress) in one write.
UPDATE exercise_import_jobs
SET state        = sqlc.arg(state),
    exercise_ids = sqlc.arg(exercise_ids)::uuid[],
    error        = sqlc.narg(error),
    stage        = sqlc.arg(stage),
    images_done  = sqlc.arg(images_done),
    images_total = sqlc.arg(images_total),
    tasks_done   = sqlc.arg(tasks_done),
    tasks_total  = sqlc.arg(tasks_total),
    bytes_pushed = sqlc.arg(bytes_pushed),
    bytes_total  = sqlc.arg(bytes_total),
    updated_at   = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);

-- name: UpdateExerciseImportJobProgress :execrows
-- The throttled progress write of a running import: a narrow write, so it never races the outcome write; a job that
-- has ended (zero rows) keeps its outcome.
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
  AND state = 'running';

-- name: ListRunningExerciseImportJobs :many
-- The imports of the user that still run (a running one silent since stale_before has stopped and is left out).
SELECT *
FROM exercise_import_jobs
WHERE created_by = sqlc.arg(created_by)
  AND state = 'running'
  AND updated_at > sqlc.arg(stale_before)
ORDER BY created_at;
