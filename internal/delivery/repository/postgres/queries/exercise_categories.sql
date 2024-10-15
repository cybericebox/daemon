-- name: GetExerciseCategories :many
select *
from exercise_categories;

-- name: CreateExerciseCategory :exec
insert into exercise_categories
    (id, name, description)
values ($1, $2, $3);

-- name: UpdateExerciseCategory :execrows
update exercise_categories
set name        = $2,
    description = $3,
    updated_at  = now(),
    updated_by  = $4
where id = $1;

-- name: DeleteExerciseCategory :execrows
delete
from exercise_categories
where id = $1;

