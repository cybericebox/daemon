-- name: CreateMediaUpload :one
INSERT INTO media_uploads (id, created_by, name, content_type, size_bytes, chunk_bytes, created_at, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: GetMediaUpload :one
SELECT *
FROM media_uploads
WHERE id = $1;

-- name: AdvanceMediaUpload :execrows
-- A chunk is accepted only as the next one in order; zero rows means the client is out of step (it reads the
-- status and continues from chunks_received). Every accepted chunk moves the expiry forward.
UPDATE media_uploads
SET chunks_received = chunks_received + 1,
    expires_at      = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id)
  AND created_by = sqlc.arg(created_by)
  AND chunks_received = sqlc.arg(expected_chunks);

-- name: DeleteMediaUpload :execrows
DELETE
FROM media_uploads
WHERE id = $1;

-- name: ListExpiredMediaUploads :many
SELECT *
FROM media_uploads
WHERE expires_at < $1
ORDER BY expires_at
LIMIT $2;
