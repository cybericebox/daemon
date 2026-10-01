-- name: CreateFile :one
-- One statement: bump (or create) the blob's refcount and insert the logical
-- file row pointing at it. Also (re)stamps the blob's touched_at to this
-- call's `now` — both on first insert and on a dedup-hit conflict — so GC's
-- orphan listing (ListOrphanBlobs) never targets a blob a concurrent upload
-- just referenced (see file_blobs.touched_at, migration 0015).
WITH blob AS (
    INSERT INTO file_blobs (content_hash, size_bytes, ref_count, created_at, touched_at)
        VALUES (sqlc.arg(content_hash), sqlc.arg(size_bytes), 1, sqlc.arg(created_at), sqlc.arg(created_at))
        ON CONFLICT (content_hash) DO UPDATE SET ref_count = file_blobs.ref_count + 1, touched_at = excluded.touched_at)
INSERT
INTO files (id, name, content_type, size_bytes, content_hash, created_at, created_by)
VALUES (sqlc.arg(id), sqlc.arg(name), sqlc.arg(content_type), sqlc.arg(size_bytes), sqlc.arg(content_hash),
        sqlc.arg(created_at), sqlc.arg(created_by))
RETURNING *;

-- name: GetFileByID :one
SELECT *
FROM files
WHERE id = $1;

-- name: BlobExists :one
SELECT EXISTS(SELECT 1 FROM file_blobs WHERE content_hash = $1)::boolean;

-- name: ReplaceFileReferences :exec
-- Diff-style sync: drop references not in the new set, insert missing ones.
-- Kept rows are never touched, so the delete CTE cannot collide with the
-- insert (same-statement visibility).
WITH del AS (
    DELETE FROM file_references
        WHERE ref_type = sqlc.arg(ref_type) AND ref_id = sqlc.arg(ref_id)
            AND NOT (file_id = ANY (sqlc.arg(file_ids)::uuid[])))
INSERT
INTO file_references (ref_type, ref_id, file_id)
SELECT sqlc.arg(ref_type), sqlc.arg(ref_id), unnest(sqlc.arg(file_ids)::uuid[])
ON CONFLICT DO NOTHING;

-- name: GetFileReferenceIDs :many
-- Read path for an owner's current file set (e.g. resolving a user's avatar
-- file before streaming it). Ordered newest-file-first (files.created_at
-- DESC, file_id DESC as a deterministic tiebreak): a concurrent double-upload
-- can leave more than one reference row for the same owner, and a caller that
-- wants "the current" one (e.g. GetAvatar picking index 0) needs a
-- deterministic, not incidental, answer. Repositories don't validate on read:
-- a ref_type/ref_id with no rows simply yields an empty slice, not an error.
SELECT r.file_id
FROM file_references r
         JOIN files f ON f.id = r.file_id
WHERE r.ref_type = $1
  AND r.ref_id = $2
ORDER BY f.created_at DESC, r.file_id DESC;

-- name: AddFileReference :exec
-- An email image is owned by its draft as soon as it is uploaded. Inserting
-- one link avoids a read/replace race when uploads finish concurrently.
INSERT INTO file_references (ref_type, ref_id, file_id)
VALUES ($1, $2, $3)
ON CONFLICT DO NOTHING;

-- name: DeleteFileReferences :execrows
DELETE
FROM file_references
WHERE ref_type = $1
  AND ref_id = $2;

-- name: DeleteFileReferencesBatch :execrows
DELETE
FROM file_references
WHERE ref_type = $1
  AND ref_id = ANY (sqlc.arg(ref_ids)::uuid[]);

-- name: DeleteUnreferencedFiles :many
-- GC step 1: drop grace-expired unreferenced files, decrement their blobs.
WITH doomed AS (
    DELETE FROM files f
        WHERE f.created_at < sqlc.arg(created_before)
            AND NOT EXISTS (SELECT 1 FROM file_references r WHERE r.file_id = f.id)
        RETURNING f.content_hash),
     counted AS (SELECT content_hash, count(*) AS cnt FROM doomed GROUP BY content_hash)
UPDATE file_blobs b
SET ref_count = b.ref_count - counted.cnt
FROM counted
WHERE b.content_hash = counted.content_hash
RETURNING b.content_hash, b.ref_count;

-- name: ListOrphanBlobs :many
-- touched_at < touched_before (the same GC grace cutoff DeleteUnreferencedFiles
-- uses) gives a concurrent dedup upload of this exact content a grace window
-- to complete its CreateFile bump before GC ever considers the blob a
-- deletion candidate — see file_blobs.touched_at, migration 0015.
SELECT content_hash
FROM file_blobs
WHERE ref_count <= 0
  AND touched_at < sqlc.arg(touched_before);

-- name: DeleteBlob :execrows
-- Guarded delete: refuses when the blob got re-referenced between the S3
-- removal decision and this call.
DELETE
FROM file_blobs
WHERE content_hash = $1
  AND ref_count <= 0;
