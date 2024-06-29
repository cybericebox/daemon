-- name: CreateTemporalCode :exec
insert into temporal_codes (id, expired_at, code_type, v0, v1, v2)
values ($1, $2, $3, $4, $5, $6);

-- name: GetTemporalCode :one
select *
from temporal_codes
where id = $1;

-- name: DeleteTemporalCode :exec
delete
from temporal_codes
where id = $1;

