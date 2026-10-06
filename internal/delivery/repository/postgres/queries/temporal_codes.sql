-- name: CreateTemporalCode :one
INSERT INTO temporal_codes (id, code, type, data, expires_at)
VALUES ($1, $2, $3, $4, $5) RETURNING *;

-- name: GetTemporalCodeByCode :one
SELECT *
FROM temporal_codes
WHERE code = $1;

-- name: DeleteTemporalCode :execrows
DELETE
FROM temporal_codes
WHERE id = $1;

-- name: DeleteTemporalCodesForUser :execrows
-- Every code of the type issued to the user (the payload names the user): a
-- password reset revokes the reset links still in the mailbox.
DELETE
FROM temporal_codes
WHERE type = sqlc.arg(type)
  AND data ->> 'UserID' = sqlc.arg(user_id)::text;
