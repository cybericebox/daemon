-- name: ListExerciseVariantIssues :many
-- Teams each variant of every version of the exercise was issued to (grow-only marks).
SELECT m.exercise_version_id, m.variant_id, count(*)::bigint AS issued
FROM exercise_variant_issue_marks m
         JOIN exercise_versions v ON v.id = m.exercise_version_id
WHERE v.exercise_id = sqlc.arg(exercise_id)
GROUP BY m.exercise_version_id, m.variant_id
ORDER BY m.exercise_version_id, m.variant_id;

-- name: ListExerciseVariantTaskSolves :many
-- Teams that solved each subtask of each variant of every version of the exercise.
SELECT m.exercise_version_id, m.variant_id, m.task_id, count(*)::bigint AS solved
FROM exercise_variant_solve_marks m
         JOIN exercise_versions v ON v.id = m.exercise_version_id
WHERE v.exercise_id = sqlc.arg(exercise_id)
GROUP BY m.exercise_version_id, m.variant_id, m.task_id
ORDER BY m.exercise_version_id, m.variant_id, m.task_id;

-- name: ListExerciseVariantSolveDepths :many
-- Per variant: how many teams solved how many distinct subtasks of it (the use case compares with the variant's
-- subtask count to get the teams that solved it fully).
SELECT d.exercise_version_id, d.variant_id, d.solved_tasks, count(*)::bigint AS teams
FROM (SELECT m.exercise_version_id, m.variant_id, m.event_team_id, count(*)::bigint AS solved_tasks
      FROM exercise_variant_solve_marks m
               JOIN exercise_versions v ON v.id = m.exercise_version_id
      WHERE v.exercise_id = sqlc.arg(exercise_id)
      GROUP BY m.exercise_version_id, m.variant_id, m.event_team_id) d
GROUP BY d.exercise_version_id, d.variant_id, d.solved_tasks
ORDER BY d.exercise_version_id, d.variant_id, d.solved_tasks;
