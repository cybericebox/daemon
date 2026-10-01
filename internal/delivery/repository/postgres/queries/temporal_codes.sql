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
