-- name: ListExistingEventIDs :many
-- Which of the given events still exist (the periodic orphan LabGroup sweep).
SELECT id FROM events WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListExistingEventTeamIDs :many
-- Which of the given teams still exist (the periodic orphan LabGroup sweep).
SELECT id FROM event_teams WHERE id = ANY(sqlc.arg(ids)::uuid[]);

-- name: ListTestDeployGroupNames :many
-- Which of the given LabGroup names still hold at least one test deploy row, expired or not
-- (an expired row is cleaned up by the test deploy job, not by the sweep).
SELECT DISTINCT group_name FROM exercise_test_deployments WHERE group_name = ANY(sqlc.arg(names)::text[]);
