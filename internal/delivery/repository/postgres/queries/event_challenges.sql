-- name: CreateEventChallenge :one
INSERT INTO event_challenges (id, event_exercise_id, task_id, order_index, points, hints_enabled, published, snapshot, created_at,
                              hints, hint_costs)
VALUES (sqlc.arg(id), sqlc.arg(event_exercise_id), sqlc.arg(task_id), sqlc.arg(order_index), sqlc.arg(points),
        sqlc.arg(hints_enabled), sqlc.arg(published), sqlc.arg(snapshot), sqlc.arg(created_at),
        COALESCE(sqlc.narg(hints)::jsonb, '[]'::jsonb), COALESCE(sqlc.narg(hint_costs)::jsonb, '{}'::jsonb))
RETURNING *;

-- name: ListEventChallenges :many
SELECT *
FROM event_challenges
WHERE event_exercise_id = sqlc.arg(event_exercise_id)
ORDER BY order_index ASC;

-- name: GetEventChallengeByID :one
SELECT *
FROM event_challenges
WHERE id = sqlc.arg(id)
  AND event_exercise_id = sqlc.arg(event_exercise_id);

-- name: GetEventChallengeForEvent :one
SELECT ec.*
FROM event_challenges ec
JOIN event_exercises ee ON ee.id = ec.event_exercise_id
WHERE ec.id = sqlc.arg(id)
  AND ee.event_id = sqlc.arg(event_id);

-- name: IsEventChallengePublished :one
SELECT published FROM event_challenges WHERE id = sqlc.arg(id);

-- name: GetEventChallengeAccess :one
-- Publication and the stage phase of the task's set at the given moment (the request time), for the submission gate.
SELECT ec.published,
       ee.stage_id,
       event_stage_phase(stage.opens_at, stage.closes_at, stage.returnable, sqlc.arg(at)::timestamptz) AS phase
FROM event_challenges ec
JOIN event_exercises ee ON ee.id = ec.event_exercise_id
LEFT JOIN event_stages stage ON stage.id = ee.stage_id
WHERE ec.id = sqlc.arg(id);

-- name: UpdateEventChallenge :one
UPDATE event_challenges
SET points = sqlc.arg(points),
    hints_enabled = sqlc.arg(hints_enabled),
    published = sqlc.arg(published),
    max_flag_attempts = sqlc.narg(max_flag_attempts)
WHERE id = sqlc.arg(id)
  AND event_exercise_id = sqlc.arg(event_exercise_id)
RETURNING *;

-- name: UpdateEventChallengeScoring :one
UPDATE event_challenges
SET scoring_mode = sqlc.narg(scoring_mode),
    dynamic_algorithm = sqlc.narg(dynamic_algorithm),
    dynamic_min_points = sqlc.narg(dynamic_min_points),
    dynamic_max_points = sqlc.narg(dynamic_max_points),
    dynamic_floor_at_percent = sqlc.narg(dynamic_floor_at_percent)
WHERE id = sqlc.arg(id)
  AND event_exercise_id = sqlc.arg(event_exercise_id)
RETURNING *;

-- name: VacateEventChallengeOrders :exec
UPDATE event_challenges
SET order_index = -order_index - 1
WHERE event_exercise_id = sqlc.arg(event_exercise_id);

-- name: SetEventChallengeOrder :execrows
UPDATE event_challenges
SET order_index = sqlc.arg(order_index)
WHERE id = sqlc.arg(id)
  AND event_exercise_id = sqlc.arg(event_exercise_id);

-- name: SetEventChallengeGroup :execrows
-- A move to another group places the challenge after that group's ordered
-- challenges (board_order NULL); staying in the group keeps its position.
UPDATE event_challenges
SET board_order = CASE WHEN group_id IS NOT DISTINCT FROM sqlc.narg(group_id)::uuid THEN board_order END,
    group_id    = sqlc.narg(group_id)::uuid
WHERE id = sqlc.arg(id)
  AND event_exercise_id = sqlc.arg(event_exercise_id);

-- name: GetEventContentStatistics :one
-- Content counters intentionally read accepted solve projections, never raw
-- attempts. A rejected or later-reversed submission therefore cannot inflate
-- a landing-page statistic. Only published tasks and visible (admitted, not hidden) teams count, and, while the
-- results are frozen, only solves before the freeze (cutoff); a hidden or unpublished one is no public news.
WITH challenges AS (
    SELECT count(*)::bigint AS challenge_count,
           count(*) FILTER (WHERE ec.published)::bigint AS published_challenge_count
    FROM event_challenges ec
    JOIN event_exercises ee ON ee.id = ec.event_exercise_id
    WHERE ee.event_id = sqlc.arg(event_id)
), solves AS (
    SELECT count(DISTINCT tc.event_challenge_id)::bigint AS solved_challenge_count,
           count(*)::bigint AS solve_count
    FROM team_challenge_solves solved
    JOIN team_challenges tc ON tc.id = solved.team_challenge_id
    JOIN event_challenges ec ON ec.id = tc.event_challenge_id AND ec.published
    JOIN event_teams team ON team.id = tc.event_team_id
    WHERE tc.event_id = sqlc.arg(event_id)
      AND event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually, team.admission_locked, team.member_count)
      AND (sqlc.narg(cutoff)::timestamptz IS NULL OR solved.solved_at < sqlc.narg(cutoff)::timestamptz)
)
SELECT challenges.challenge_count,
       challenges.published_challenge_count,
       solves.solved_challenge_count,
       solves.solve_count
FROM challenges
CROSS JOIN solves;

-- name: UpdateEventChallengeContent :execrows
-- A source switch refreshes the canonical snapshot and hints in place; the
-- event's overrides (points, publish, order, group, scoring, costs) stay.
UPDATE event_challenges
SET snapshot = sqlc.arg(snapshot),
    hints    = sqlc.arg(hints)
WHERE id = sqlc.arg(id)
  AND event_exercise_id = sqlc.arg(event_exercise_id);

-- name: SetEventChallengeHintCosts :execrows
UPDATE event_challenges
SET hint_costs = sqlc.arg(hint_costs)
WHERE id = sqlc.arg(id)
  AND event_exercise_id = sqlc.arg(event_exercise_id);

-- name: ListEventExercisePrerequisites :many
-- Every prerequisite edge of one board revision in one query.
SELECT prerequisite.challenge_id, prerequisite.prerequisite_challenge_id
FROM event_challenge_prerequisites prerequisite
JOIN event_challenges ec ON ec.id = prerequisite.challenge_id
WHERE ec.event_exercise_id = sqlc.arg(event_exercise_id)
ORDER BY prerequisite.challenge_id, prerequisite.prerequisite_challenge_id;

-- name: ListEventChallengesWithAttempts :many
SELECT DISTINCT tc.event_challenge_id
FROM challenge_attempts attempt
JOIN team_challenges tc ON tc.id = attempt.team_challenge_id
WHERE tc.event_challenge_id = ANY (sqlc.arg(ids)::uuid[]);

-- name: DeleteLabBindingsForChallenges :exec
DELETE
FROM lab_bindings
WHERE event_challenge_id = ANY (sqlc.arg(ids)::uuid[]);

-- name: DeleteTeamChallengesForChallenges :exec
DELETE
FROM team_challenges
WHERE event_challenge_id = ANY (sqlc.arg(ids)::uuid[]);

-- name: DeleteEventChallengesByIDs :exec
DELETE
FROM event_challenges
WHERE id = ANY (sqlc.arg(ids)::uuid[])
  AND event_exercise_id = sqlc.arg(event_exercise_id);

-- name: UnpublishEventExerciseChallenges :exec
UPDATE event_challenges
SET published = false
WHERE event_exercise_id = sqlc.arg(event_exercise_id);

-- name: MaxEventChallengeOrder :one
SELECT COALESCE(max(order_index), -1)::integer
FROM event_challenges
WHERE event_exercise_id = sqlc.arg(event_exercise_id);

-- name: ListEventGroupChallengeIDs :many
-- Every challenge of the event's active sets in one group (NULL = no group).
SELECT ec.id
FROM event_challenges ec
JOIN event_exercises ee ON ee.id = ec.event_exercise_id
WHERE ee.event_id = sqlc.arg(event_id)
  AND ee.status = 0
  AND ec.group_id IS NOT DISTINCT FROM sqlc.narg(group_id)::uuid;

-- name: SetEventChallengeBoardOrder :execrows
UPDATE event_challenges ec
SET board_order = sqlc.arg(board_order)
FROM event_exercises ee
WHERE ec.id = sqlc.arg(id)
  AND ee.id = ec.event_exercise_id
  AND ee.event_id = sqlc.arg(event_id);

-- name: SetEventExerciseChallengesPublished :exec
-- A set is shown or hidden as a whole: its tasks share one infrastructure.
UPDATE event_challenges
SET published = sqlc.arg(published)
WHERE event_exercise_id = sqlc.arg(event_exercise_id);
