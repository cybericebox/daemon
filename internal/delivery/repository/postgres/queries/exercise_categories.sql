-- name: GetExerciseCategories :many
select *
from exercise_categories;

-- name: CreateExerciseCategory :exec
insert into exercise_categories
    (id, name, description)
values ($1, $2, $3);

-- name: UpdateExerciseCategory :exec
update exercise_categories
set name        = $2,
    description = $3
where id = $1;

-- name: DeleteExerciseCategory :exec
delete
from exercise_categories
where id = $1;

