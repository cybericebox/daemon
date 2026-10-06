-- Event analytics «Завдання» and «Прогрес» (docs/EVENT-ANALYTICS.md §6.3, §6.4).
-- The moderators team is never counted. Period bounds are [from_at, to_at).

-- name: ListEventAnalyticsChallenges :many
-- The published challenges of the event in board order, with the declared
-- difficulty from the snapshot. A NULL challenge_id lists them all.
SELECT ec.id                                            AS challenge_id,
       COALESCE(ec.snapshot ->> 'name', '')::text       AS name,
       COALESCE(ec.snapshot ->> 'difficulty', '')::text AS difficulty,
       ec.points::integer                               AS points,
       COALESCE(g.id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS group_id,
       COALESCE(g.name, '')::text                       AS group_name
FROM event_challenges ec
JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.event_id = sqlc.arg(event_id) AND ee.status <> 2
LEFT JOIN event_challenge_groups g ON g.id = ec.group_id
WHERE ec.published
  AND (sqlc.narg(challenge_id)::uuid IS NULL OR ec.id = sqlc.narg(challenge_id)::uuid)
ORDER BY (g.id IS NULL), g.order_index, COALESCE(ec.board_order, ec.order_index), ec.order_index, ec.id;

-- name: ListEventAnalyticsTaskStats :many
-- Per-task figures over the period. Times are seconds; -1 means "no solves".
-- since_open counts only solves after the team's first recorded task open.
-- First blood is the first solve of a ranked team (not hidden, admitted).
WITH teams AS (
    SELECT t.id,
           event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS name,
           (event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)) AS counted
    FROM event_teams t
    WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators
), challenges AS (
    SELECT ec.id
    FROM event_challenges ec
    JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.event_id = sqlc.arg(event_id) AND ee.status <> 2
    WHERE ec.published
), attempts AS (
    SELECT tc.event_challenge_id AS challenge_id, a.event_team_id AS team_id, a.effective_correct
    FROM effective_challenge_attempts a
    JOIN teams ON teams.id = a.event_team_id
    JOIN team_challenges tc ON tc.id = a.team_challenge_id
    WHERE a.event_id = sqlc.arg(event_id)
      AND a.received_at >= sqlc.arg(from_at)::timestamptz
      AND a.received_at < sqlc.arg(to_at)::timestamptz
), attempt_totals AS (
    SELECT challenge_id,
           count(*)                                  AS attempts,
           count(*) FILTER (WHERE effective_correct) AS attempts_correct,
           count(DISTINCT team_id)                   AS teams_tried
    FROM attempts
    GROUP BY challenge_id
), solves AS (
    SELECT tc.event_challenge_id AS challenge_id, tc.event_team_id AS team_id, tc.id AS team_challenge_id, s.solved_at
    FROM team_challenge_solves s
    JOIN team_challenges tc ON tc.id = s.team_challenge_id
    JOIN teams ON teams.id = tc.event_team_id
    WHERE tc.event_id = sqlc.arg(event_id)
      AND s.solved_at >= sqlc.arg(from_at)::timestamptz
      AND s.solved_at < sqlc.arg(to_at)::timestamptz
), first_open AS (
    SELECT o.subject_id AS challenge_id, o.team_id, min(o.at) AS at
    FROM event_activity o
    JOIN teams ON teams.id = o.team_id
    WHERE o.event_id = sqlc.arg(event_id) AND o.kind = 'task_opened' AND o.subject_id IS NOT NULL
    GROUP BY o.subject_id, o.team_id
), opened AS (
    SELECT challenge_id, count(*) AS teams_opened FROM first_open GROUP BY challenge_id
), solve_times AS (
    SELECT s.challenge_id,
           EXTRACT(EPOCH FROM (s.solved_at - ev.start_at)) AS since_start,
           CASE WHEN fo.at IS NOT NULL AND fo.at <= s.solved_at
                    THEN EXTRACT(EPOCH FROM (s.solved_at - fo.at)) END AS since_open
    FROM solves s
    JOIN events ev ON ev.id = sqlc.arg(event_id)
    LEFT JOIN first_open fo ON fo.challenge_id = s.challenge_id AND fo.team_id = s.team_id
), solve_totals AS (
    SELECT challenge_id,
           count(*) AS solves,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY since_start) AS median_since_start,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY since_open)  AS median_since_open
    FROM solve_times
    GROUP BY challenge_id
), first_blood AS (
    SELECT DISTINCT ON (s.challenge_id) s.challenge_id, teams.name AS team_name, s.solved_at
    FROM solves s
    JOIN teams ON teams.id = s.team_id
    WHERE teams.counted
    ORDER BY s.challenge_id, s.solved_at, s.team_challenge_id
), hints AS (
    SELECT h.event_challenge_id AS challenge_id, count(*) AS hints_opened, COALESCE(sum(h.cost), 0) AS hint_points
    FROM team_challenge_hint_unlocks h
    JOIN teams ON teams.id = h.event_team_id
    WHERE h.event_id = sqlc.arg(event_id)
      AND h.unlocked_at >= sqlc.arg(from_at)::timestamptz
      AND h.unlocked_at < sqlc.arg(to_at)::timestamptz
    GROUP BY h.event_challenge_id
)
SELECT c.id                                                   AS challenge_id,
       COALESCE(at.attempts, 0)::bigint                       AS attempts,
       COALESCE(at.attempts_correct, 0)::bigint               AS attempts_correct,
       COALESCE(at.teams_tried, 0)::bigint                    AS teams_tried,
       COALESCE(op.teams_opened, 0)::bigint                   AS teams_opened,
       COALESCE(st.solves, 0)::bigint                         AS solves,
       COALESCE(round(st.median_since_start), -1)::bigint     AS median_since_start,
       COALESCE(round(st.median_since_open), -1)::bigint      AS median_since_open,
       COALESCE(fb.team_name, '')::text                       AS first_blood_team,
       fb.solved_at                                           AS first_blood_at,
       COALESCE(h.hints_opened, 0)::bigint                    AS hints_opened,
       COALESCE(h.hint_points, 0)::bigint                     AS hint_points
FROM challenges c
LEFT JOIN attempt_totals at ON at.challenge_id = c.id
LEFT JOIN opened op ON op.challenge_id = c.id
LEFT JOIN solve_totals st ON st.challenge_id = c.id
LEFT JOIN first_blood fb ON fb.challenge_id = c.id
LEFT JOIN hints h ON h.challenge_id = c.id;

-- name: ListEventAnalyticsTaskSeries :many
-- One challenge's 5-minute activity over the period, from the rollup.
SELECT bucket_at,
       sum(attempts)::bigint AS attempts,
       sum(correct)::bigint  AS correct,
       sum(solves)::bigint   AS solves,
       sum(opens)::bigint    AS opens
FROM event_activity_buckets
WHERE event_id = sqlc.arg(event_id)
  AND challenge_id = sqlc.arg(challenge_id)
  AND bucket_at >= sqlc.arg(from_at)::timestamptz
  AND bucket_at < sqlc.arg(to_at)::timestamptz
GROUP BY bucket_at
ORDER BY bucket_at;

-- name: ListEventAnalyticsTaskFailedTeams :many
-- Teams with attempts on the challenge in the period and no solve of it at
-- all, the most persistent first.
WITH teams AS (
    SELECT t.id, event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS name
    FROM event_teams t
    WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators
), tried AS (
    SELECT a.event_team_id AS team_id, count(*) AS attempts, max(a.received_at) AS last_attempt_at
    FROM effective_challenge_attempts a
    JOIN team_challenges tc ON tc.id = a.team_challenge_id
    WHERE a.event_id = sqlc.arg(event_id)
      AND tc.event_challenge_id = sqlc.arg(challenge_id)
      AND a.received_at >= sqlc.arg(from_at)::timestamptz
      AND a.received_at < sqlc.arg(to_at)::timestamptz
    GROUP BY a.event_team_id
)
SELECT teams.id                                       AS team_id,
       teams.name                                     AS team_name,
       tried.attempts::bigint                         AS attempts,
       tried.last_attempt_at::timestamptz             AS last_attempt_at,
       (SELECT count(*) FROM team_challenge_hint_unlocks h
        WHERE h.event_team_id = teams.id AND h.event_challenge_id = sqlc.arg(challenge_id))::bigint AS hints_opened
FROM tried
JOIN teams ON teams.id = tried.team_id
WHERE NOT EXISTS (SELECT 1
                  FROM team_challenge_solves s
                  JOIN team_challenges tc ON tc.id = s.team_challenge_id
                  WHERE tc.event_team_id = teams.id AND tc.event_challenge_id = sqlc.arg(challenge_id))
ORDER BY tried.attempts DESC, teams.name, teams.id;

-- name: ListEventAnalyticsTaskWrongAnswers :many
-- The most common wrong answers of a challenge in the period (sensitive:
-- answer texts). The text is cut to 200 characters.
SELECT left(a.answer, 200)::text                AS answer,
       count(*)::bigint                         AS attempts,
       count(DISTINCT a.event_team_id)::bigint  AS teams,
       max(a.received_at)::timestamptz          AS last_at
FROM effective_challenge_attempts a
JOIN event_teams t ON t.id = a.event_team_id AND NOT t.moderators
JOIN team_challenges tc ON tc.id = a.team_challenge_id
WHERE a.event_id = sqlc.arg(event_id)
  AND tc.event_challenge_id = sqlc.arg(challenge_id)
  AND NOT a.effective_correct
  AND a.received_at >= sqlc.arg(from_at)::timestamptz
  AND a.received_at < sqlc.arg(to_at)::timestamptz
GROUP BY left(a.answer, 200)
ORDER BY count(*) DESC, max(a.received_at) DESC, left(a.answer, 200)
LIMIT sqlc.arg(row_limit);

-- name: ListEventAnalyticsTaskHintEffect :many
-- One row per team that tried the challenge in the period. hinted: a hint was
-- unlocked before the solve (or the team never solved). since_hint_secs is
-- the solve time after the first unlock (hinted teams); since_start_secs is
-- the solve time after the team's first open or attempt (others). -1: none.
WITH teams AS (
    SELECT t.id FROM event_teams t WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators
), tried AS (
    SELECT a.event_team_id AS team_id, min(a.received_at) AS first_attempt_at
    FROM effective_challenge_attempts a
    JOIN teams ON teams.id = a.event_team_id
    JOIN team_challenges tc ON tc.id = a.team_challenge_id
    WHERE a.event_id = sqlc.arg(event_id)
      AND tc.event_challenge_id = sqlc.arg(challenge_id)
      AND a.received_at >= sqlc.arg(from_at)::timestamptz
      AND a.received_at < sqlc.arg(to_at)::timestamptz
    GROUP BY a.event_team_id
), team_facts AS (
    SELECT tried.team_id,
           tried.first_attempt_at,
           (SELECT min(o.at) FROM event_activity o
            WHERE o.event_id = sqlc.arg(event_id) AND o.team_id = tried.team_id
              AND o.kind = 'task_opened' AND o.subject_id = sqlc.arg(challenge_id)) AS first_open_at,
           (SELECT min(h.unlocked_at) FROM team_challenge_hint_unlocks h
            WHERE h.event_team_id = tried.team_id AND h.event_challenge_id = sqlc.arg(challenge_id)) AS first_unlock_at,
           (SELECT min(s.solved_at)
            FROM team_challenge_solves s
            JOIN team_challenges tc ON tc.id = s.team_challenge_id
            WHERE tc.event_team_id = tried.team_id AND tc.event_challenge_id = sqlc.arg(challenge_id)
              AND s.solved_at >= sqlc.arg(from_at)::timestamptz
              AND s.solved_at < sqlc.arg(to_at)::timestamptz) AS solved_at
    FROM tried
)
SELECT f.team_id,
       (f.first_unlock_at IS NOT NULL AND (f.solved_at IS NULL OR f.first_unlock_at <= f.solved_at))::boolean AS hinted,
       (f.solved_at IS NOT NULL)::boolean AS solved,
       (CASE WHEN f.solved_at IS NOT NULL AND f.first_unlock_at IS NOT NULL AND f.first_unlock_at <= f.solved_at
                 THEN round(EXTRACT(EPOCH FROM (f.solved_at - f.first_unlock_at)))
             ELSE -1 END)::bigint AS since_hint_secs,
       (CASE WHEN f.solved_at IS NOT NULL
                 THEN round(EXTRACT(EPOCH FROM (f.solved_at - LEAST(f.first_attempt_at, COALESCE(f.first_open_at, f.first_attempt_at)))))
             ELSE -1 END)::bigint AS since_start_secs
FROM team_facts f;

-- name: ListEventAnalyticsScoreEvents :many
-- Every score change (solves and hint penalties) of the given teams, oldest
-- first: the running total is the sum of Points, as on the scoreboard.
SELECT s.event_team_id::uuid        AS team_id,
       s.solved_at::timestamptz     AS at,
       s.points::integer            AS points
FROM event_solved_scores_at(sqlc.arg(event_id)::uuid, NULL, NULL) s
WHERE s.event_team_id = ANY (sqlc.arg(team_ids)::uuid[])
ORDER BY 2 ASC, 1 ASC;

-- name: ListEventAnalyticsMatrix :many
-- The team × task cells that were touched in the period: attempts, and the
-- solve time when solved. Untouched cells are not returned.
WITH teams AS (
    SELECT t.id FROM event_teams t WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators
), tried AS (
    SELECT a.event_team_id AS team_id, tc.event_challenge_id AS challenge_id, count(*) AS attempts
    FROM effective_challenge_attempts a
    JOIN teams ON teams.id = a.event_team_id
    JOIN team_challenges tc ON tc.id = a.team_challenge_id
    WHERE a.event_id = sqlc.arg(event_id)
      AND a.received_at >= sqlc.arg(from_at)::timestamptz
      AND a.received_at < sqlc.arg(to_at)::timestamptz
    GROUP BY a.event_team_id, tc.event_challenge_id
), solved AS (
    SELECT tc.event_team_id AS team_id, tc.event_challenge_id AS challenge_id, s.solved_at
    FROM team_challenge_solves s
    JOIN team_challenges tc ON tc.id = s.team_challenge_id
    JOIN teams ON teams.id = tc.event_team_id
    WHERE tc.event_id = sqlc.arg(event_id)
      AND s.solved_at >= sqlc.arg(from_at)::timestamptz
      AND s.solved_at < sqlc.arg(to_at)::timestamptz
)
SELECT COALESCE(tried.team_id, solved.team_id)::uuid           AS team_id,
       COALESCE(tried.challenge_id, solved.challenge_id)::uuid AS challenge_id,
       COALESCE(tried.attempts, 0)::bigint                     AS attempts,
       solved.solved_at                                        AS solved_at
FROM tried
FULL JOIN solved ON solved.team_id = tried.team_id AND solved.challenge_id = tried.challenge_id;

-- name: ListEventAnalyticsHeatmap :many
-- Team activity per hour over the period, from the 5-minute rollup.
SELECT date_bin('1 hour', bucket_at, 'epoch'::timestamptz)::timestamptz AS hour_at,
       team_id,
       sum(attempts)::bigint AS attempts,
       sum(opens)::bigint    AS opens,
       sum(solves)::bigint   AS solves
FROM event_activity_buckets
WHERE event_id = sqlc.arg(event_id)
  AND bucket_at >= sqlc.arg(from_at)::timestamptz
  AND bucket_at < sqlc.arg(to_at)::timestamptz
GROUP BY 1, team_id
HAVING sum(attempts) + sum(opens) + sum(solves) > 0
ORDER BY 1, team_id;

-- name: ListEventAnalyticsTeamActivity :many
-- The last sign of life of every ranked team (not hidden, admitted): an
-- attempt, a task open or download, a hint unlock, or lab access over the VPN
-- or the proxy, up to as_of. The epoch
-- means the team has done nothing yet.
WITH teams AS (
    SELECT t.id,
           event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS name
    FROM event_teams t
    WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
)
SELECT teams.id AS team_id,
       teams.name AS team_name,
       COALESCE((SELECT max(x.at) FROM (
            SELECT max(a.received_at) AS at FROM effective_challenge_attempts a
            WHERE a.event_id = sqlc.arg(event_id) AND a.event_team_id = teams.id AND a.received_at <= sqlc.arg(as_of)::timestamptz
            UNION ALL
            SELECT max(o.at) FROM event_activity o
            WHERE o.event_id = sqlc.arg(event_id) AND o.team_id = teams.id
              AND o.kind IN ('task_opened', 'attachment_downloaded') AND o.at <= sqlc.arg(as_of)::timestamptz
            UNION ALL
            SELECT max(h.unlocked_at) FROM team_challenge_hint_unlocks h
            WHERE h.event_id = sqlc.arg(event_id) AND h.event_team_id = teams.id AND h.unlocked_at <= sqlc.arg(as_of)::timestamptz
            UNION ALL
            SELECT max(l.last_seen_at) FROM event_lab_touches l
            WHERE l.event_id = sqlc.arg(event_id) AND l.team_id = teams.id AND l.last_seen_at <= sqlc.arg(as_of)::timestamptz
            UNION ALL
            SELECT max(v.ended_at) FROM event_vpn_sessions v
            WHERE v.event_id = sqlc.arg(event_id) AND v.team_id = teams.id AND v.ended_at <= sqlc.arg(as_of)::timestamptz
        ) x), 'epoch'::timestamptz)::timestamptz AS last_activity_at
FROM teams
ORDER BY teams.name, teams.id;
