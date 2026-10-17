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

-- name: UpsertImageBlobPush :exec
-- The whole push state of one blob in one statement (queued, failed).
INSERT INTO image_blob_pushes (digest, state, reason, updated_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (digest) DO UPDATE
SET state      = EXCLUDED.state,
    reason     = EXCLUDED.reason,
    updated_at = EXCLUDED.updated_at;

-- name: GetImageBlobPush :one
SELECT *
FROM image_blob_pushes
WHERE digest = $1;

-- name: DeleteImageBlobPush :exec
DELETE
FROM image_blob_pushes
WHERE digest = $1;
