-- name: CreateChallengeAttempt :one
INSERT INTO challenge_attempts (id, event_id, event_team_id, team_challenge_id, user_id, answer, correct, received_at, created_at)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(event_team_id), sqlc.arg(team_challenge_id), sqlc.arg(user_id),
        sqlc.arg(answer), sqlc.arg(correct), sqlc.arg(received_at), sqlc.arg(created_at))
RETURNING *;

-- name: GetEventSolutionAttemptCursor :one
SELECT id, received_at
FROM challenge_attempts
WHERE id = sqlc.arg(id)
  AND event_id = sqlc.arg(event_id);

-- name: GetEventSolutionAttemptForDecision :one
-- Locking the team challenge serializes a manual verdict with submission-time
-- scoring, so the derived solved_at remains a faithful projection.
SELECT ca.id,
       ca.event_id,
       ca.event_team_id,
       ca.team_challenge_id,
       tc.event_challenge_id,
       ca.correct,
       ca.received_at
FROM challenge_attempts ca
JOIN team_challenges tc ON tc.id = ca.team_challenge_id
WHERE ca.id = sqlc.arg(id)
  AND ca.event_id = sqlc.arg(event_id)
FOR UPDATE OF ca, tc;

-- name: CreateChallengeAttemptDecision :one
INSERT INTO challenge_attempt_decisions (id, challenge_attempt_id, decision, reason, decided_by, decided_at)
VALUES (sqlc.arg(id), sqlc.arg(challenge_attempt_id), sqlc.arg(decision), sqlc.arg(reason), sqlc.arg(decided_by), sqlc.arg(decided_at))
RETURNING *;

-- name: ListEventSolutionAttempts :many
SELECT ca.id,
       ca.event_id,
       ca.event_team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       ca.team_challenge_id,
       ec.id AS event_challenge_id,
       COALESCE(ec.snapshot->>'name', '')::text AS challenge_name,
       ee.id AS event_exercise_id,
       ca.user_id,
       concat_ws(' ', u.first_name, u.last_name)::text AS participant_name,
       ca.answer,
       tc.expected_flag,
       ca.automatic_correct,
       ca.decision,
       ca.effective_correct AS correct,
       ca.decision_reason,
       ca.decision_id AS latest_decision_id,
       ca.decided_by,
       ca.decided_at,
       ca.received_at,
       (score.team_challenge_id IS NOT NULL)::bool AS scored,
       COALESCE(score.points, 0)::integer AS points,
       COALESCE(ec.max_flag_attempts, cfg.max_flag_attempts) AS attempts_allowed,
       used.wrong::bigint AS attempts_used
FROM effective_challenge_attempts ca
JOIN event_teams t ON t.id = ca.event_team_id
JOIN team_challenges tc ON tc.id = ca.team_challenge_id
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
LEFT JOIN event_configs cfg ON cfg.event_id = ca.event_id
CROSS JOIN LATERAL (
    SELECT count(*) FILTER (WHERE NOT x.effective_correct AND (x.first_correct IS NULL OR x.received_at < x.first_correct)) AS wrong
    FROM (SELECT a.effective_correct, a.received_at,
                 min(a.received_at) FILTER (WHERE a.effective_correct) OVER () AS first_correct
          FROM effective_challenge_attempts a
          WHERE a.team_challenge_id = ca.team_challenge_id) x
) used
JOIN event_exercises ee ON ee.id = ec.event_exercise_id
JOIN users u ON u.id = ca.user_id
LEFT JOIN event_solved_scores_at(sqlc.arg(event_id)::uuid, NULL, NULL) score
       ON score.solve AND ca.effective_correct
      AND score.team_challenge_id = ca.team_challenge_id AND score.solved_at = ca.received_at
WHERE ca.event_id = sqlc.arg(event_id)::uuid
  AND (sqlc.narg(team_id)::uuid IS NULL OR ca.event_team_id = sqlc.narg(team_id)::uuid)
  AND (sqlc.narg(participant_id)::uuid IS NULL OR ca.user_id = sqlc.narg(participant_id)::uuid)
  AND (sqlc.narg(challenge_id)::uuid IS NULL OR ec.id = sqlc.narg(challenge_id)::uuid)
  AND (sqlc.narg(correct)::bool IS NULL OR ca.effective_correct = sqlc.narg(correct)::bool)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR ca.received_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR ca.received_at < sqlc.narg(to_at)::timestamptz)
  AND (ca.received_at, ca.id) < (sqlc.arg(cursor_received_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY ca.received_at DESC, ca.id DESC
LIMIT sqlc.arg(limit_val);

-- name: CountEventSolutionAttempts :one
SELECT count(*)
FROM effective_challenge_attempts ca
JOIN team_challenges tc ON tc.id = ca.team_challenge_id
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
WHERE ca.event_id = sqlc.arg(event_id)::uuid
  AND (sqlc.narg(team_id)::uuid IS NULL OR ca.event_team_id = sqlc.narg(team_id)::uuid)
  AND (sqlc.narg(participant_id)::uuid IS NULL OR ca.user_id = sqlc.narg(participant_id)::uuid)
  AND (sqlc.narg(challenge_id)::uuid IS NULL OR ec.id = sqlc.narg(challenge_id)::uuid)
  AND (sqlc.narg(correct)::bool IS NULL OR ca.effective_correct = sqlc.narg(correct)::bool)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR ca.received_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR ca.received_at < sqlc.narg(to_at)::timestamptz);

-- name: ListTeamResultAttempts :many
SELECT ca.id,
       ca.event_team_id,
       ca.user_id,
       event_participant_public_name(ca.event_id, ca.user_id)::text AS participant_name,
       ca.team_challenge_id,
       ec.id AS event_challenge_id,
       ca.answer,
       ca.automatic_correct,
       ca.effective_correct AS correct,
       ca.decision,
       ca.decision_reason,
       ca.decided_by,
       ca.decided_at,
       ca.received_at
FROM effective_challenge_attempts ca
JOIN users u ON u.id = ca.user_id
JOIN team_challenges tc ON tc.id = ca.team_challenge_id
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
WHERE ca.event_id = sqlc.arg(event_id)
  AND ca.event_team_id = sqlc.arg(event_team_id)
ORDER BY ca.received_at DESC, ca.id DESC;

-- name: GetEffectiveTeamChallengeSolvedAt :one
SELECT EXISTS(
           SELECT 1
           FROM effective_challenge_attempts a
           WHERE a.team_challenge_id = sqlc.arg(team_challenge_id)
             AND a.effective_correct
       ) AS solved,
       COALESCE((
           SELECT received_at
           FROM effective_challenge_attempts a
           WHERE a.team_challenge_id = sqlc.arg(team_challenge_id)
             AND a.effective_correct
           ORDER BY a.received_at ASC, a.id ASC
           LIMIT 1
       ), 'epoch'::timestamptz)::timestamptz AS solved_at;

-- name: GetTeamChallengeScoringContext :one
SELECT tc.event_id,
       tc.event_challenge_id,
       (CASE WHEN e.static_points IS NOT NULL AND e.scoring_mode = 0
                  AND (e.force_event_scoring OR ec.scoring_mode IS NULL)
             THEN e.static_points ELSE ec.points END)::integer AS static_points,
       ec.scoring_mode AS local_scoring_mode,
       ec.dynamic_min_points AS local_min_points,
       ec.dynamic_max_points AS local_max_points,
       ec.dynamic_floor_at_percent AS local_floor_at_percent,
       e.scoring_mode AS event_scoring_mode,
       e.dynamic_min_points AS event_min_points,
       e.dynamic_max_points AS event_max_points,
       e.dynamic_floor_at_percent AS event_floor_at_percent,
       e.force_event_scoring,
       e.start_at,
       e.finish_at,
       population.units_count
FROM team_challenges tc
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
JOIN events e ON e.id = tc.event_id
LEFT JOIN event_scoring_populations population ON population.event_id = e.id
WHERE tc.id = sqlc.arg(team_challenge_id);

-- name: UpsertTeamChallengeSolve :exec
INSERT INTO team_challenge_solves (team_challenge_id, solved_at, awarded_points)
VALUES (sqlc.arg(team_challenge_id), sqlc.arg(solved_at), sqlc.narg(awarded_points))
ON CONFLICT (team_challenge_id) DO UPDATE
SET solved_at = EXCLUDED.solved_at,
    awarded_points = COALESCE(team_challenge_solves.awarded_points, EXCLUDED.awarded_points);

-- name: DeleteTeamChallengeSolve :exec
DELETE FROM team_challenge_solves
WHERE team_challenge_id = sqlc.arg(team_challenge_id);

-- name: LockEventTeamChallenge :one
-- Locks one team challenge so an annulment serializes with submission-time
-- scoring and single decisions (they lock the same row).
SELECT tc.id
FROM team_challenges tc
WHERE tc.event_id = sqlc.arg(event_id)
  AND tc.event_team_id = sqlc.arg(event_team_id)
  AND tc.event_challenge_id = sqlc.arg(event_challenge_id)
FOR UPDATE;

-- name: ListEffectiveCorrectAttemptIDs :many
SELECT a.id
FROM effective_challenge_attempts a
WHERE a.team_challenge_id = sqlc.arg(team_challenge_id)
  AND a.effective_correct
ORDER BY a.received_at ASC, a.id ASC;

-- name: GetEventAttemptsStamp :one
-- Cheap change marker for the moderators' live journal: attempts, decisions
-- and opened hints.
SELECT (SELECT count(*) FROM challenge_attempts ca WHERE ca.event_id = sqlc.arg(event_id))::bigint AS attempts,
       (SELECT count(*)
        FROM challenge_attempt_decisions d
        JOIN challenge_attempts ca ON ca.id = d.challenge_attempt_id
        WHERE ca.event_id = sqlc.arg(event_id))::bigint AS decisions,
       (SELECT count(*) FROM team_challenge_hint_unlocks u WHERE u.event_id = sqlc.arg(event_id))::bigint AS hint_unlocks;

-- name: GetTeamChallengeAttemptWindow :one
-- Rate limit: the latest attempts (at most row_limit) of one team challenge
-- received after since, and the oldest of them — when it leaves the window
-- the next attempt is allowed.
SELECT count(*)::bigint AS attempts,
       COALESCE(min(recent.received_at), 'epoch'::timestamptz)::timestamptz AS oldest
FROM (SELECT ca.received_at
      FROM challenge_attempts ca
      WHERE ca.team_challenge_id = sqlc.arg(team_challenge_id)
        AND ca.received_at > sqlc.arg(since)
      ORDER BY ca.received_at DESC
      LIMIT sqlc.arg(row_limit)) recent;

-- name: GetTeamAttemptWindow :one
-- Rate limit: the same window over every challenge of one team.
SELECT count(*)::bigint AS attempts,
       COALESCE(min(recent.received_at), 'epoch'::timestamptz)::timestamptz AS oldest
FROM (SELECT ca.received_at
      FROM challenge_attempts ca
      WHERE ca.event_id = sqlc.arg(event_id)
        AND ca.event_team_id = sqlc.arg(event_team_id)
        AND ca.received_at > sqlc.arg(since)
      ORDER BY ca.received_at DESC
      LIMIT sqlc.arg(row_limit)) recent;


-- name: GetTeamChallengeAttemptLimit :one
-- The flag attempt limit of one team challenge (the task's own value, else the event's; NULL = unlimited) and the
-- wrong submissions counted against it. Only wrong attempts received before the first effective correct one count:
-- once a task is solved nothing more is counted, and after an annulment those later attempts stay uncounted.
SELECT COALESCE(ec.max_flag_attempts, cfg.max_flag_attempts) AS max_attempts,
       s.wrong::bigint AS wrong,
       s.solved::bool AS solved
FROM team_challenges tc
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
LEFT JOIN event_configs cfg ON cfg.event_id = tc.event_id
CROSS JOIN LATERAL (
    SELECT count(*) FILTER (WHERE NOT x.effective_correct AND (x.first_correct IS NULL OR x.received_at < x.first_correct)) AS wrong,
           COALESCE(bool_or(x.first_correct IS NOT NULL), false) AS solved
    FROM (SELECT a.effective_correct, a.received_at,
                 min(a.received_at) FILTER (WHERE a.effective_correct) OVER () AS first_correct
          FROM effective_challenge_attempts a
          WHERE a.team_challenge_id = tc.id) x
) s
WHERE tc.id = sqlc.arg(team_challenge_id);

-- name: ListTeamChallengeAttemptLimits :many
-- GetTeamChallengeAttemptLimit for every limited team challenge of one team (the participant board).
SELECT tc.id AS team_challenge_id,
       COALESCE(ec.max_flag_attempts, cfg.max_flag_attempts) AS max_attempts,
       s.wrong::bigint AS wrong,
       s.solved::bool AS solved
FROM team_challenges tc
JOIN event_challenges ec ON ec.id = tc.event_challenge_id
LEFT JOIN event_configs cfg ON cfg.event_id = tc.event_id
CROSS JOIN LATERAL (
    SELECT count(*) FILTER (WHERE NOT x.effective_correct AND (x.first_correct IS NULL OR x.received_at < x.first_correct)) AS wrong,
           COALESCE(bool_or(x.first_correct IS NOT NULL), false) AS solved
    FROM (SELECT a.effective_correct, a.received_at,
                 min(a.received_at) FILTER (WHERE a.effective_correct) OVER () AS first_correct
          FROM effective_challenge_attempts a
          WHERE a.team_challenge_id = tc.id) x
) s
WHERE tc.event_team_id = sqlc.arg(event_team_id)
  AND COALESCE(ec.max_flag_attempts, cfg.max_flag_attempts) IS NOT NULL;
