-- Background exports of tasks with device images (the export job aggregate: one row per export).

-- name: CreateExerciseExportJob :exec
INSERT INTO exercise_export_jobs (id, created_by, exercise_ids, file_name, state, file_id, error, created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(created_by), sqlc.arg(exercise_ids)::uuid[], sqlc.arg(file_name), sqlc.arg(state),
        sqlc.narg(file_id), sqlc.narg(error), sqlc.arg(created_at), sqlc.arg(updated_at));

-- name: GetExerciseExportJob :one
SELECT *
FROM exercise_export_jobs
WHERE id = sqlc.arg(id);

-- name: UpdateExerciseExportJob :execrows
-- The whole job aggregate (state, stored file, error) in one write.
UPDATE exercise_export_jobs
SET state      = sqlc.arg(state),
    file_id    = sqlc.narg(file_id),
    error      = sqlc.narg(error),
    updated_at = sqlc.arg(updated_at)
WHERE id = sqlc.arg(id);
