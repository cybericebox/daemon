-- name: CreateExerciseTestDeploy :one
INSERT INTO exercise_test_deployments (id, group_name, lab_name, version_id, variant_id, created_by, created_at, expires_at, flags)
VALUES (sqlc.arg(id), sqlc.arg(group_name), sqlc.arg(lab_name), sqlc.arg(version_id), sqlc.arg(variant_id), sqlc.arg(created_by), sqlc.arg(created_at), sqlc.arg(expires_at), sqlc.arg(flags)) RETURNING *;
-- name: GetOwnedExerciseTestDeploy :one
SELECT * FROM exercise_test_deployments WHERE id = sqlc.arg(id) AND created_by = sqlc.arg(created_by);
-- name: DeleteOwnedExerciseTestDeploy :execrows
DELETE FROM exercise_test_deployments WHERE id = sqlc.arg(id) AND created_by = sqlc.arg(created_by);
-- name: ListExpiredExerciseTestDeploys :many
SELECT * FROM exercise_test_deployments WHERE expires_at <= sqlc.arg(expires_at) ORDER BY expires_at;
-- name: ListOwnedExerciseTestDeploys :many
SELECT * FROM exercise_test_deployments WHERE created_by = sqlc.arg(created_by) ORDER BY expires_at;
-- name: ListOwnedExerciseTestDeploysForExercise :many
SELECT d.* FROM exercise_test_deployments d
JOIN exercise_versions v ON v.id = d.version_id
WHERE d.created_by = sqlc.arg(created_by) AND v.exercise_id = sqlc.arg(exercise_id)
ORDER BY d.expires_at;
-- name: ExtendOwnedExerciseTestDeploy :one
UPDATE exercise_test_deployments SET expires_at = sqlc.arg(expires_at)
WHERE id = sqlc.arg(id) AND created_by = sqlc.arg(created_by) RETURNING *;
-- name: LockExerciseTestDeploysOf :exec
-- Serializes the "one active test lab per user" check and insert of one owner until the transaction ends.
SELECT pg_advisory_xact_lock(hashtextextended('exercise-test-deploy:' || sqlc.arg(owner)::text, 0));
-- name: CountActiveExerciseTestDeploys :one
SELECT count(*) FROM exercise_test_deployments WHERE created_by = sqlc.arg(created_by) AND expires_at > sqlc.arg(now);
-- name: MarkExerciseTestDeploySolved :one
-- Adds a task the author checked correctly; idempotent (no duplicates).
UPDATE exercise_test_deployments
SET solved = CASE WHEN solved @> jsonb_build_array(sqlc.arg(task_id)::text) THEN solved ELSE solved || jsonb_build_array(sqlc.arg(task_id)::text) END
WHERE id = sqlc.arg(id) AND created_by = sqlc.arg(created_by) RETURNING *;
