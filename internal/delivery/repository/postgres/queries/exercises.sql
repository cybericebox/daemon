-- name: GetExercises :many
select *
from exercises
order by lower(name)
limit $1 offset $2;

-- name: GetExercisesWithIDs :many
select *
from exercises
where id = any (@ids::uuid[])
order by name;

-- name: GetExercisesNotWithIDs :many
select *
from exercises
where id <> all (@ids::uuid[])
order by lower(name)
limit $1 offset $2;

-- name: GetExercisesByCategory :many
select *
from exercises
where category_id = $1
order by lower(name)
limit $2 offset $3;

-- name: GetExercisesWithSimilarName :many
select *
from exercises
where lower(name) like '%' || lower(@search::text) || '%'
   or lower(description) like '%' || lower(@search::text) || '%'
order by lower(name)
limit $1 offset $2;


-- name: GetExercisesNotWithIDsWithSimilarName :many
select *
from exercises
where lower(name) like '%' || lower(@search::text) || '%'
   or lower(description) like '%' || lower(@search::text) || '%'
    and id <> all (@ids::uuid[])
order by lower(name)
limit $1 offset $2;

-- name: GetExerciseByID :one
select *
from exercises
where id = $1;

-- name: CreateExercise :exec
insert into exercises
    (id, category_id, name, description, data)
values ($1, $2, $3, $4, $5);

-- name: UpdateExercise :execrows
update exercises
set category_id = $2,
    name        = $3,
    description = $4,
    data       = $5,
    updated_at = now(),
    updated_by = $6
where id = $1;

-- name: DeleteExercise :execrows
delete
from exercises
where id = $1;