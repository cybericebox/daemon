-- A stored file still counts as the task's copy only while the media GC would keep it: some reference holds it,
-- or it is younger than the GC grace cutoff (the same rule DeleteUnreferencedFiles applies). A file past that
-- point may vanish at any moment, so it is neither offered by the lookup nor kept by a claim.

-- name: GetExerciseFileByHash :one
-- The task's stored file for a content, if any. Scoped to the one task: never reveals another task's files.
SELECT f.id, f.name, f.size_bytes
FROM exercise_files ef
         JOIN files f ON f.id = ef.file_id
WHERE ef.exercise_id = sqlc.arg(exercise_id)
  AND ef.content_hash = sqlc.arg(content_hash)
  AND (f.created_at >= sqlc.arg(grace_cutoff)::timestamptz
    OR EXISTS (SELECT 1 FROM file_references r WHERE r.file_id = f.id));

-- name: ClaimExerciseFile :one
-- Records the file as the task's copy of its content and returns the task's canonical file. On conflict the
-- stored file stays canonical while the GC would keep it; a GC-eligible one is replaced by the given file.
INSERT INTO exercise_files AS ef (exercise_id, content_hash, file_id, created_at)
VALUES (sqlc.arg(exercise_id), sqlc.arg(content_hash), sqlc.arg(file_id), sqlc.arg(created_at))
ON CONFLICT (exercise_id, content_hash) DO UPDATE
    SET file_id    = CASE
                         WHEN EXISTS (SELECT 1 FROM file_references r WHERE r.file_id = ef.file_id)
                             OR EXISTS (SELECT 1
                                        FROM files f
                                        WHERE f.id = ef.file_id
                                          AND f.created_at >= sqlc.arg(grace_cutoff)::timestamptz)
                             THEN ef.file_id
                         ELSE EXCLUDED.file_id END,
        created_at = CASE
                         WHEN EXISTS (SELECT 1 FROM file_references r WHERE r.file_id = ef.file_id)
                             OR EXISTS (SELECT 1
                                        FROM files f
                                        WHERE f.id = ef.file_id
                                          AND f.created_at >= sqlc.arg(grace_cutoff)::timestamptz)
                             THEN ef.created_at
                         ELSE EXCLUDED.created_at END
RETURNING file_id;

-- name: ListFileHashes :many
-- The content hash of each of the files; a file that does not exist is absent.
SELECT id, content_hash
FROM files
WHERE id = ANY (sqlc.arg(ids)::uuid[]);

-- name: ListExerciseHeldFiles :many
-- Which of the files the task stores as its copy of a content (a co-author's upload found by the hash lookup).
SELECT file_id
FROM exercise_files
WHERE exercise_id = sqlc.arg(exercise_id)
  AND file_id = ANY (sqlc.arg(file_ids)::uuid[]);
