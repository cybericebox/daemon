-- name: GetExercises :many
select *
from exercises
order by name;

-- name: GetExercisesByIDs :many
select *
from exercises
where id = any (@ids::uuid[])
order by name;

-- name: GetExercisesByCategory :many
select *
from exercises
where category_id = $1
order by name;

-- name: GetExercisesWithSimilarName :many
select *
from exercises
where name ilike '%' || @search::text || '%'
   or description ilike '%' || @search::text || '%'
order by name;

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