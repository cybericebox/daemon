-- name: GetExerciseFileByHash :one
-- The task's stored file for a content, if any. Scoped to the one task: never reveals another task's files.
SELECT f.id, f.name, f.size_bytes
FROM exercise_files ef
         JOIN files f ON f.id = ef.file_id
WHERE ef.exercise_id = sqlc.arg(exercise_id)
  AND ef.content_hash = sqlc.arg(content_hash);

-- name: ClaimExerciseFile :one
-- Records the file as the task's copy of its content, unless the task already holds that content: either way it
-- returns the task's canonical file. The no-op update makes RETURNING yield the existing row on conflict.
INSERT INTO exercise_files (exercise_id, content_hash, file_id, created_at)
VALUES (sqlc.arg(exercise_id), sqlc.arg(content_hash), sqlc.arg(file_id), sqlc.arg(created_at))
ON CONFLICT (exercise_id, content_hash) DO UPDATE SET content_hash = EXCLUDED.content_hash
RETURNING file_id;

-- name: ListFileHashes :many
-- The content hash of each of the files; a file that does not exist is absent.
SELECT id, content_hash
FROM files
WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ExerciseHoldsFile :one
-- Whether the task stores the file as its copy of a content (a co-author's upload found by the hash lookup).
SELECT EXISTS(SELECT 1
              FROM exercise_files
              WHERE exercise_id = sqlc.arg(exercise_id)
                AND file_id = sqlc.arg(file_id))::boolean;
