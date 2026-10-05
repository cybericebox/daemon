-- name: GetLabBinding :one
SELECT * FROM lab_bindings WHERE event_team_id = sqlc.arg(event_team_id) AND event_challenge_id = sqlc.arg(event_challenge_id);

-- name: CreateLabBinding :one
INSERT INTO lab_bindings (id,event_id,event_team_id,event_challenge_id,lab_group_name,lab_name,created_at,generation)
VALUES (sqlc.arg(id),sqlc.arg(event_id),sqlc.arg(event_team_id),sqlc.arg(event_challenge_id),sqlc.arg(lab_group_name),sqlc.arg(lab_name),sqlc.arg(created_at),sqlc.arg(generation))
ON CONFLICT (event_team_id, event_challenge_id) DO NOTHING
RETURNING *;

-- name: UpdateLabBindingReadiness :execrows
UPDATE lab_bindings
SET readiness = sqlc.arg(readiness)
WHERE id = sqlc.arg(id)
  AND readiness = sqlc.arg(expected_readiness);

-- name: ListWithdrawnLabBindings :many
-- Lab groups are retained through finish, then become eligible for final teardown
-- only once the event is withdrawn. A destroyed binding never re-enters this
-- queue; a failed agent call leaves it eligible for River's next retry.
SELECT lb.*
FROM lab_bindings lb
JOIN events e ON e.id = lb.event_id
WHERE e.withdraw_at IS NOT NULL
  AND e.withdraw_at <= sqlc.arg(now)
  AND lb.readiness <> 3
ORDER BY lb.lab_group_name, lb.id;

-- name: MarkLabBindingDestroyed :execrows
UPDATE lab_bindings
SET readiness = 3
WHERE id = sqlc.arg(id)
  AND readiness <> 3;

-- name: QueueTeamLabGroupCleanup :execrows
-- Queue the deterministic team LabGroup even when no challenge Lab has been
-- attached yet. The group may already contain VPN clients and a gateway.
INSERT INTO lab_group_cleanup_requests (lab_group_name, event_id, requested_at)
SELECT lab_group_name(team.event_id, team.id),
       team.event_id, sqlc.arg(requested_at)
FROM event_teams team
WHERE team.id = sqlc.arg(event_team_id)
ON CONFLICT (lab_group_name) DO NOTHING;

-- name: QueueWithdrawnEmptyLabGroups :exec
-- A team can own VPN clients and a gateway before it has any challenge Lab.
-- On withdrawal, make those groups eligible for the same durable cleanup job.
INSERT INTO lab_group_cleanup_requests (lab_group_name, event_id, requested_at)
SELECT lab_group_name(team.event_id, team.id),
       team.event_id, sqlc.arg(now)::timestamptz
FROM event_teams team
JOIN events event ON event.id = team.event_id
WHERE event.withdraw_at IS NOT NULL
  AND event.withdraw_at <= sqlc.arg(now)::timestamptz
  AND NOT EXISTS (SELECT 1 FROM lab_bindings lb WHERE lb.event_team_id = team.id)
ON CONFLICT (lab_group_name) DO NOTHING;

-- name: ListPendingLabGroupCleanupRequests :many
SELECT lab_group_name
FROM lab_group_cleanup_requests
WHERE destroyed_at IS NULL
ORDER BY requested_at, lab_group_name;

-- name: MarkLabGroupCleanupRequestDestroyed :execrows
UPDATE lab_group_cleanup_requests
SET destroyed_at = sqlc.arg(destroyed_at)
WHERE lab_group_name = sqlc.arg(lab_group_name)
  AND destroyed_at IS NULL;

-- name: ListPendingEventLabBindings :many
-- Stand engine work queue: every not yet ready Lab of one event together with
-- the pinned version and variant needed to resolve its topology.
SELECT lb.id, lb.event_team_id, lb.event_challenge_id, lb.lab_group_name, lb.lab_name,
       lb.generation, lb.deployed_at, lb.created_at,
       tc.variant_index, ee.exercise_version_id
FROM lab_bindings lb
JOIN team_challenges tc ON tc.event_team_id = lb.event_team_id
                       AND tc.event_challenge_id = lb.event_challenge_id
JOIN event_challenges ec ON ec.id = lb.event_challenge_id
JOIN event_exercises ee ON ee.id = ec.event_exercise_id
WHERE lb.event_id = sqlc.arg(event_id)
  AND lb.readiness = 0
  -- Labs of a later stage wait until their deploy lead before they open.
  AND ee.id <> ALL (sqlc.arg(not_due_exercise_ids)::uuid[])
ORDER BY lb.created_at, lb.id;

-- name: MarkLabBindingDeployed :execrows
UPDATE lab_bindings
SET deployed_at = sqlc.arg(deployed_at)
WHERE id = sqlc.arg(id)
  AND generation = sqlc.arg(generation)
  AND readiness = 0
  AND deployed_at IS NULL;

-- name: MarkLabBindingFailedWithReason :execrows
UPDATE lab_bindings
SET readiness = 2,
    failure_reason = sqlc.arg(failure_reason)
WHERE id = sqlc.arg(id)
  AND generation = sqlc.arg(generation)
  AND readiness = 0;

-- name: MarkStandLabReady :one
-- A ready Lab makes its preparing team challenge ready in the same statement.
-- Returns the number of bindings changed (0 = stale generation or repeat).
WITH lab AS (
    UPDATE lab_bindings binding
    SET readiness = 1
    WHERE binding.id = sqlc.arg(id)
      AND binding.generation = sqlc.arg(generation)
      AND binding.readiness = 0
    RETURNING binding.event_team_id, binding.event_challenge_id
), challenge AS (
    UPDATE team_challenges tc
    SET readiness = 1
    FROM lab
    WHERE tc.event_team_id = lab.event_team_id
      AND tc.event_challenge_id = lab.event_challenge_id
      AND tc.readiness = 0
    RETURNING tc.id
)
SELECT count(*)::bigint FROM lab;

-- name: ListTeamInfrastructureLabs :many
SELECT *
FROM lab_bindings
WHERE event_team_id = sqlc.arg(event_team_id)
  AND readiness <> 3
ORDER BY lab_name;

-- name: RecreateLabBinding :execrows
-- A recreated Lab moves to the next generation under a new name, so the
-- asynchronously deleted previous Lab never collides, and deploys again.
UPDATE lab_bindings
SET generation = generation + 1,
    lab_name = sqlc.arg(lab_name),
    readiness = 0,
    deployed_at = NULL,
    failure_reason = NULL
WHERE id = sqlc.arg(id)
  AND generation = sqlc.arg(generation)
  AND readiness <> 3;

-- name: ResetUnpublishedTeamChallengesForRecreate :exec
-- Not yet published infrastructure challenges wait for the recreated Lab;
-- already published ones stay on the team's board (the ACL still requires a
-- ready Lab, so access returns only once the new Lab is ready).
UPDATE team_challenges tc
SET readiness = 0
FROM lab_bindings lb
WHERE lb.event_team_id = sqlc.arg(event_team_id)
  AND lb.readiness <> 3
  AND tc.event_team_id = lb.event_team_id
  AND tc.event_challenge_id = lb.event_challenge_id
  AND tc.readiness = 1;

-- name: MarkEventLabBindingsDestroyed :exec
UPDATE lab_bindings
SET readiness = 3
WHERE event_id = sqlc.arg(event_id)
  AND readiness <> 3;
