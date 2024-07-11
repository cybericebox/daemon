-- name: DoesUserExistByID :one
select exists(select 1 from users where id = $1) as exists;

-- name: CreateUser :exec
insert into users (id, google_id, email, name, hashed_password, picture, role)
values ($1, $2, $3, $4, $5, $6, $7);

-- name: GetAllUsers :many
select id, google_id, email, name, picture, role, last_seen, updated_at, updated_by, created_at
from users
order by name;

-- name: GetUsersWithSimilar :many
select id, google_id, email, name, picture, role, last_seen, updated_at, updated_by, created_at
from users
where name ilike '%' || @search::text || '%'
   or email ilike '%' || @search || '%'
order by name;

-- name: GetUserByEmail :one
select *
from users
where email = $1;

-- name: GetUserByID :one
select *
from users
where id = $1;

-- name: SetLastSeen :exec
update users
set last_seen = now()
where id = $1;

-- name: UpdateUserGoogleID :exec
update users
set google_id  = $2,
    updated_at = now()
where id = $1;

-- name: UpdateUserName :exec
update users
set name       = $2,
    updated_at = now()
where id = $1;

-- name: UpdateUserPicture :exec
update users
set picture    = $2,
    updated_at = now()
where id = $1;

-- name: UpdateUserEmail :exec
update users
set email      = $2,
    updated_at = now()
where id = $1;

-- name: UpdateUserPassword :exec
update users
set hashed_password = $2,
    updated_at      = now()
where id = $1;

-- name: UpdateUserRole :exec
update users
set role       = $2,
    updated_at = now()
where id = $1;

-- name: DeleteUser :exec
delete
from users
where id = $1;

