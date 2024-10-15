-- name: CountUsers :one
select count(*) as count
from users;

-- name: GetAllUsers :many
select id,
       google_id,
       email,
       name,
       picture,
       role,
       last_seen,
       updated_at,
       updated_by,
       created_at
from users
order by name;

-- name: GetUsersWithSimilar :many
select id,
       google_id,
       email,
       name,
       picture,
       role,
       last_seen,
       updated_at,
       updated_by,
       created_at
from users
where name ilike '%' || @search::text || '%'
   or email ilike '%' || @search::text || '%'
order by name;

-- name: GetUserByEmail :one
select *
from users
where email = $1;

-- name: GetUserByID :one
select *
from users
where id = $1;

-- name: CreateUser :exec
insert into users (id, google_id, email, name, hashed_password, picture, role)
values ($1, $2, $3, $4, $5, $6, $7);

-- name: SetLastSeen :execrows
update users
set last_seen = now()
where id = $1;

-- name: UpdateUserGoogleID :execrows
update users
set google_id  = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateUserName :execrows
update users
set name       = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateUserPicture :execrows
update users
set picture    = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateUserEmail :execrows
update users
set email      = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateUserPassword :execrows
update users
set hashed_password = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateUserRole :execrows
update users
set role       = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: DeleteUser :execrows
delete
from users
where id = $1;

