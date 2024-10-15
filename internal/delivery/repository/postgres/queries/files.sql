-- name: GetFiles :many
select *
from files
order by created_at;

-- name: CreateFile :exec
insert into files (id, storage_type)
values ($1, $2);

-- name: DeleteFile :batchexec
delete
from files
where id = $1;