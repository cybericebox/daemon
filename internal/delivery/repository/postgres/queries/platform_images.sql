-- name: UpsertPlatformImage :exec
-- One row per pushed tag. Uploading the same image again (same digest and tag) refreshes the row: the upload time
-- (the tag collector's 24 h grace starts again), the exercise and the uploader.
INSERT INTO platform_images (digest, tag, name, exercise_id, uploaded_by, uploaded_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (digest, tag) DO UPDATE
SET name        = EXCLUDED.name,
    exercise_id = EXCLUDED.exercise_id,
    uploaded_by = EXCLUDED.uploaded_by,
    uploaded_at = EXCLUDED.uploaded_at;
