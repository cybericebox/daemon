-- name: CreateTemporalCode :exec
insert into temporal_codes (id, expired_at, code_type, data)
values ($1, $2, $3, $4);

-- name: GetTemporalCode :one
select *
from temporal_codes
where id = $1;

-- name: DeleteTemporalCode :execrows
delete
from temporal_codes
where id = $1;

-- name: DeleteExpiredTemporalCodes :execrows
delete
from temporal_codes
where expired_at < now();

