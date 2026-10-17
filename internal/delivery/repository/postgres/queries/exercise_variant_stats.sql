-- name: ListExerciseVariantStats :many
-- Grow-only issue and solve counters of every version of the exercise.
SELECT s.exercise_version_id, s.variant_id, s.issued, s.solved
FROM exercise_variant_stats s
         JOIN exercise_versions v ON v.id = s.exercise_version_id
WHERE v.exercise_id = sqlc.arg(exercise_id)
ORDER BY s.exercise_version_id, s.variant_id;

-- name: ListExerciseVariantTaskStats :many
-- Grow-only per-subtask solve counters of every version of the exercise.
SELECT s.exercise_version_id, s.variant_id, s.task_id, s.solved
FROM exercise_variant_task_stats s
         JOIN exercise_versions v ON v.id = s.exercise_version_id
WHERE v.exercise_id = sqlc.arg(exercise_id)
ORDER BY s.exercise_version_id, s.variant_id, s.task_id;
