-- name: GetFileByID :one
select *
from files
where id = $1;

-- name: CreateFile :exec
insert into files (id, name)
values ($1, $2);

-- name: DeleteFile :exec
delete
from files
where id = $1;