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

-- name: ListUnreferencedPlatformImages :many
-- Every row (tag) of the platform digests the tag collector may remove: no exercise version (draft, published,
-- any status) has a device image platform:<name>@<digest> (the name may differ), and every row of the digest was
-- uploaded before $1. Variants that are not an array, or a "devices" that is null or not an array, hold no image.
SELECT pi.*
FROM platform_images pi
WHERE NOT EXISTS (SELECT 1
                  FROM platform_images fresh
                  WHERE fresh.digest = pi.digest
                    AND fresh.uploaded_at >= sqlc.arg(before)::timestamptz)
  AND NOT EXISTS (SELECT 1
                  FROM exercise_versions v,
                       jsonb_array_elements(CASE WHEN jsonb_typeof(v.variants) = 'array' THEN v.variants ELSE '[]'::jsonb END) AS variant,
                       jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) AS device
                  WHERE jsonb_typeof(device -> 'image') = 'string'
                    AND (device ->> 'image') LIKE 'platform:%'
                    AND split_part(device ->> 'image', '@', 2) = pi.digest)
ORDER BY pi.digest, pi.tag;

-- name: DeletePlatformImage :exec
DELETE
FROM platform_images
WHERE digest = $1
  AND tag = $2;

-- name: DeleteStaleImageBlobPushes :execrows
-- Push states nobody came back to: failed ones, and pushing ones whose job is long gone.
DELETE
FROM image_blob_pushes
WHERE updated_at < $1;
