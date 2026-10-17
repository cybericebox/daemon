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

-- name: PlatformImageTagTakenByOtherDigest :one
-- Whether a row of another digest already maps this tag: CreateImage refuses then instead of re-pointing the tag.
SELECT EXISTS (SELECT 1
               FROM platform_images
               WHERE tag = sqlc.arg(tag)::text
                 AND digest <> sqlc.arg(digest)::text) AS taken;

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
-- Rows (tags) of the platform digests the tag collector may remove: every row of the digest was uploaded before
-- $1 (the grace filter, applied first), and no exercise version (draft, published, any status) has a device image
-- platform:<name>@<digest> (the name may differ). Variants that are not an array, or a "devices" that is null or
-- not an array, hold no image. A digest with a tag that a row of another digest also maps (a shared tag: only a bug or
-- an old short tag makes one) is never a candidate: removing that tag could remove what serves the other digest; its
-- rows stay until the other digest's row goes. At most 500 rows a pass; the rest wait for the next one.
WITH candidates AS (SELECT digest
                    FROM platform_images
                    GROUP BY digest
                    HAVING max(uploaded_at) < sqlc.arg(before)::timestamptz),
     referenced AS (SELECT DISTINCT split_part(device ->> 'image', '@', 2) AS digest
                    FROM exercise_versions v,
                         jsonb_array_elements(CASE WHEN jsonb_typeof(v.variants) = 'array' THEN v.variants ELSE '[]'::jsonb END) AS variant,
                         jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) AS device
                    WHERE jsonb_typeof(device -> 'image') = 'string'
                      AND (device ->> 'image') LIKE 'platform:%'),
     shared AS (SELECT DISTINCT own.digest
                FROM platform_images own
                         JOIN platform_images other ON other.tag = own.tag AND other.digest <> own.digest)
SELECT pi.*
FROM platform_images pi
         JOIN candidates c ON c.digest = pi.digest
         LEFT JOIN referenced r ON r.digest = pi.digest
         LEFT JOIN shared s ON s.digest = pi.digest
WHERE r.digest IS NULL
  AND s.digest IS NULL
ORDER BY pi.digest, pi.tag
LIMIT 500;

-- name: IsDigestCollectable :one
-- The list query's predicate for one digest, asked again right before its tags are removed: nothing references it
-- now, no row of it was uploaded at/after $2, and none of its tags is shared with another digest.
SELECT EXISTS (SELECT 1 FROM platform_images WHERE digest = sqlc.arg(target_digest)::text)
       AND NOT EXISTS (SELECT 1 FROM platform_images WHERE digest = sqlc.arg(target_digest)::text AND uploaded_at >= sqlc.arg(before)::timestamptz)
       AND NOT EXISTS (SELECT 1
                       FROM platform_images own
                                JOIN platform_images other ON other.tag = own.tag AND other.digest <> own.digest
                       WHERE own.digest = sqlc.arg(target_digest)::text)
       AND NOT EXISTS (SELECT 1
                       FROM exercise_versions v,
                            jsonb_array_elements(CASE WHEN jsonb_typeof(v.variants) = 'array' THEN v.variants ELSE '[]'::jsonb END) AS variant,
                            jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) AS device
                       WHERE jsonb_typeof(device -> 'image') = 'string'
                         AND (device ->> 'image') LIKE 'platform:%'
                         AND split_part(device ->> 'image', '@', 2) = sqlc.arg(target_digest)::text)::boolean AS collectable;

-- name: DeletePlatformImage :execrows
-- Removes the record of one tag only while its digest is still collectable (same predicate as IsDigestCollectable).
DELETE
FROM platform_images pi
WHERE pi.digest = sqlc.arg(digest)
  AND pi.tag = sqlc.arg(tag)
  AND NOT EXISTS (SELECT 1 FROM platform_images fresh WHERE fresh.digest = pi.digest AND fresh.uploaded_at >= sqlc.arg(before)::timestamptz)
  AND NOT EXISTS (SELECT 1
                  FROM platform_images own
                           JOIN platform_images other ON other.tag = own.tag AND other.digest <> own.digest
                  WHERE own.digest = pi.digest)
  AND NOT EXISTS (SELECT 1
                  FROM exercise_versions v,
                       jsonb_array_elements(CASE WHEN jsonb_typeof(v.variants) = 'array' THEN v.variants ELSE '[]'::jsonb END) AS variant,
                       jsonb_array_elements(CASE WHEN jsonb_typeof(variant #> '{topology,devices}') = 'array' THEN variant #> '{topology,devices}' ELSE '[]'::jsonb END) AS device
                  WHERE jsonb_typeof(device -> 'image') = 'string'
                    AND (device ->> 'image') LIKE 'platform:%'
                    AND split_part(device ->> 'image', '@', 2) = pi.digest);

-- name: DeleteStaleImageBlobPushes :execrows
-- Push states nobody came back to: failed ones, and pushing ones whose job is long gone.
DELETE
FROM image_blob_pushes
WHERE updated_at < $1;
