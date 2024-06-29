-- name: GetExercises :many
select *
from exercises;

-- name: GetExercisesByCategory :many
select *
from exercises
where category_id = $1;

-- name: GetExerciseByID :one
select *
from exercises
where id = $1;

-- name: CreateExercise :exec
insert into exercises
    (id, category_id, name, description, data)
values ($1, $2, $3, $4, $5);

-- name: UpdateExercise :exec
update exercises
set category_id = $2,
    name        = $3,
    description = $4,
    data        = $5
where id = $1;

-- name: DeleteExercise :exec
delete
from exercises
where id = $1;