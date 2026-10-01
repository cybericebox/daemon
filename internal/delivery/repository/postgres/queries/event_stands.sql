-- name: ListStandEvents :many
-- Events inside the stand window: configured, live, past start minus the
-- deploy lead, and not yet torn down. The schedule is re-derived every tick,
-- so lifecycle and setting changes need no rescheduling.
SELECT event.id
FROM events event
JOIN event_configs config ON config.event_id = event.id
LEFT JOIN event_stand_rollouts rollout ON rollout.event_id = event.id
WHERE event.lifecycle_configured
  AND (event.archive_at IS NULL OR sqlc.arg(now)::timestamptz < event.archive_at)
  AND (event.withdraw_at IS NULL OR sqlc.arg(now)::timestamptz < event.withdraw_at)
  AND event.start_at - make_interval(mins => config.stand_deploy_lead_minutes) <= sqlc.arg(now)::timestamptz
  AND rollout.torn_down_at IS NULL
ORDER BY event.start_at, event.id;

-- name: GetEventStandRollout :one
SELECT *
FROM event_stand_rollouts
WHERE event_id = sqlc.arg(event_id);

-- name: OpenEventStandRollout :exec
INSERT INTO event_stand_rollouts (event_id, opened_at, updated_at)
VALUES (sqlc.arg(event_id), sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz)
ON CONFLICT (event_id) DO UPDATE
SET opened_at = COALESCE(event_stand_rollouts.opened_at, EXCLUDED.opened_at),
    updated_at = EXCLUDED.updated_at;

-- name: TearDownEventStandRollout :exec
INSERT INTO event_stand_rollouts (event_id, torn_down_at, updated_at)
VALUES (sqlc.arg(event_id), sqlc.arg(now)::timestamptz, sqlc.arg(now)::timestamptz)
ON CONFLICT (event_id) DO UPDATE
SET torn_down_at = COALESCE(event_stand_rollouts.torn_down_at, EXCLUDED.torn_down_at),
    updated_at = EXCLUDED.updated_at;

-- name: CreateModeratorsTeam :exec
-- The hidden moderators team: captained by the event owner, admitted and
-- locked, excluded from every participant, scoring and team-management read.
INSERT INTO event_teams (id, event_id, name, join_code, captain_id, hidden, member_count, created_at, updated_at,
                         individual, admitted_manually, admission_locked, moderators, formed_at)
SELECT sqlc.arg(id), manager.event_id, sqlc.arg(name), sqlc.arg(join_code), manager.user_id, true, 1,
       sqlc.arg(now), sqlc.arg(now), false, true, true, true, sqlc.arg(now)
FROM event_managers manager
WHERE manager.event_id = sqlc.arg(event_id)
  AND manager.role = 0
ORDER BY manager.created_at, manager.user_id
LIMIT 1
ON CONFLICT DO NOTHING;

-- name: GetModeratorsTeam :one
SELECT *
FROM event_teams
WHERE event_id = sqlc.arg(event_id)
  AND moderators;

-- name: GetStandTeam :one
-- Any team of the event, including the moderators team.
SELECT *
FROM event_teams
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: ListMissingTeamAssignments :many
-- (team, active exercise) pairs the engine still has to prepare: an admitted
-- team (or the moderators team) lacks a team challenge, or has a preparing
-- one without its Lab binding while infrastructure is allowed.
SELECT DISTINCT team.id AS event_team_id, exercise.id AS event_exercise_id
FROM event_teams team
JOIN event_exercises exercise ON exercise.event_id = team.event_id AND exercise.status = 0
JOIN event_challenges challenge ON challenge.event_exercise_id = exercise.id
LEFT JOIN team_challenges assignment ON assignment.event_team_id = team.id
                                    AND assignment.event_challenge_id = challenge.id
WHERE team.event_id = sqlc.arg(event_id)
  AND (team.moderators OR event_team_admitted(team.event_id, team.individual, team.admitted_manually,
                                              team.admission_locked, team.member_count))
  AND (team.moderators OR event_team_stand_wanted(team.event_id, team.formed_at))
  AND (assignment.id IS NULL
    OR (sqlc.arg(infrastructure_allowed)::boolean
        AND assignment.readiness = 0
        AND NOT EXISTS (SELECT 1
                        FROM lab_bindings lb
                        WHERE lb.event_team_id = team.id
                          AND lb.event_challenge_id = challenge.id)))
ORDER BY event_team_id, event_exercise_id;

-- name: ListStandTeams :many
-- Stand candidates (admitted teams, the moderators team and any team that
-- already has a stand) with their persisted stand and Lab counters.
SELECT team.id, team.name, team.individual, team.moderators, team.captain_id,
       event_team_public_name(team.individual, team.event_id, team.captain_id, team.name)::text AS public_name,
       (team.moderators OR event_team_admitted(team.event_id, team.individual, team.admitted_manually,
                                               team.admission_locked, team.member_count))::boolean AS admitted,
       stand.status AS stand_status, stand.reason AS stand_reason, stand.generation AS stand_generation,
       stand.updated_at AS stand_updated_at,
       (SELECT count(*) FROM lab_bindings lb WHERE lb.event_team_id = team.id AND lb.readiness = 0)::bigint AS pending_labs,
       (SELECT count(*) FROM lab_bindings lb WHERE lb.event_team_id = team.id AND lb.readiness = 2)::bigint AS failed_labs,
       COALESCE((SELECT min(lb.failure_reason) FROM lab_bindings lb
                 WHERE lb.event_team_id = team.id AND lb.readiness = 2), '')::text AS failure_reason,
       (SELECT COALESCE(max(lb.generation), 0) FROM lab_bindings lb WHERE lb.event_team_id = team.id)::integer AS lab_generation,
       (SELECT count(*)
        FROM event_exercises exercise
        JOIN event_challenges challenge ON challenge.event_exercise_id = exercise.id
        WHERE exercise.event_id = team.event_id
          AND exercise.status = 0
          AND NOT EXISTS (SELECT 1 FROM team_challenges assignment
                          WHERE assignment.event_team_id = team.id
                            AND assignment.event_challenge_id = challenge.id))::bigint AS missing_assignments
FROM event_teams team
LEFT JOIN event_team_stands stand ON stand.event_team_id = team.id
WHERE team.event_id = sqlc.arg(event_id)
  AND (team.moderators OR stand.event_team_id IS NOT NULL
    OR (event_team_admitted(team.event_id, team.individual, team.admitted_manually,
                            team.admission_locked, team.member_count)
        AND event_team_stand_wanted(team.event_id, team.formed_at)))
ORDER BY team.moderators DESC, public_name, team.id;

-- name: CreateEventTeamStand :execrows
INSERT INTO event_team_stands (event_team_id, event_id, status, reason, generation, created_at, updated_at, status_changed_at)
VALUES (sqlc.arg(event_team_id), sqlc.arg(event_id), sqlc.arg(status), sqlc.narg(reason), sqlc.arg(generation),
        sqlc.arg(now), sqlc.arg(now), sqlc.arg(now))
ON CONFLICT (event_team_id) DO NOTHING;

-- name: UpdateEventTeamStand :execrows
-- Conditional on the previously read status, so a concurrent engine pass or a
-- recreate cannot be overwritten and a failure transition fires only once.
-- A removed stand is terminal.
UPDATE event_team_stands
SET status = sqlc.arg(status),
    reason = sqlc.narg(reason),
    generation = sqlc.arg(generation),
    updated_at = sqlc.arg(now),
    status_changed_at = CASE WHEN status <> sqlc.arg(status) THEN sqlc.arg(now) ELSE status_changed_at END
WHERE event_team_id = sqlc.arg(event_team_id)
  AND status = sqlc.arg(expected_status)
  AND status <> 4;

-- name: RemoveEventTeamStands :exec
UPDATE event_team_stands
SET status = 4,
    reason = NULL,
    updated_at = sqlc.arg(now),
    status_changed_at = sqlc.arg(now)
WHERE event_id = sqlc.arg(event_id)
  AND status <> 4;

-- name: ListEventTeamLabGroups :many
-- Every team LabGroup the event may own (VPN-only groups included).
SELECT (lab_group_name(team.event_id, team.id))::text AS lab_group_name
FROM event_teams team
WHERE team.event_id = sqlc.arg(event_id)
ORDER BY team.id;

-- name: ListEventStandLabs :many
SELECT lb.event_team_id, lb.event_challenge_id, lb.readiness, lb.failure_reason,
       COALESCE(challenge.snapshot->>'name', '')::text AS challenge_name
FROM lab_bindings lb
JOIN event_challenges challenge ON challenge.id = lb.event_challenge_id
WHERE lb.event_id = sqlc.arg(event_id)
ORDER BY lb.event_team_id, challenge.order_index, lb.event_challenge_id;

-- name: ListModeratorsTeamChallenges :many
SELECT assignment.event_challenge_id, assignment.readiness,
       COALESCE(challenge.snapshot->>'name', '')::text AS challenge_name,
       lb.readiness AS lab_readiness
FROM team_challenges assignment
JOIN event_teams team ON team.id = assignment.event_team_id
JOIN event_challenges challenge ON challenge.id = assignment.event_challenge_id
JOIN event_exercises exercise ON exercise.id = challenge.event_exercise_id
LEFT JOIN lab_bindings lb ON lb.event_team_id = assignment.event_team_id
                         AND lb.event_challenge_id = assignment.event_challenge_id
WHERE team.event_id = sqlc.arg(event_id)
  AND team.moderators
  AND exercise.status = 0
ORDER BY challenge.order_index, assignment.event_challenge_id;

-- name: ListEventStandRecipients :many
-- Every event manager (read or write access) and every active platform
-- administrator receives stand failures, once each.
SELECT DISTINCT recipient.user_id::uuid AS user_id
FROM (
    SELECT m.user_id
    FROM event_managers m
    WHERE m.event_id = sqlc.arg(event_id)
    UNION
    SELECT u.id
    FROM users u
    WHERE u.role IN ('super_admin', 'admin')
      AND u.status = 'active'
      AND u.deleted_at IS NULL
) recipient
ORDER BY 1;

-- name: GetEventTeamStand :one
SELECT *
FROM event_team_stands
WHERE event_team_id = sqlc.arg(event_team_id);
