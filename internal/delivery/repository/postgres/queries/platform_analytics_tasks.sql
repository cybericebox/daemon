-- Platform analytics «Каталог завдань» (docs/EVENT-ANALYTICS.md §8). Usage and
-- calibration of catalog tasks across the events that start in the period
-- [from_at, to_at). The catalog is the exercises table (scope 0); a task is
-- one (exercise, task) pair, an event challenge is its use in one event. The
-- category is an exercise tag, the level is the task difficulty. The
-- moderators team is never counted.

-- name: ListPlatformAnalyticsTaskCatalog :many
-- One row per catalog task used in the period, most used first (at most
-- row_limit; total is the size of the whole result). Definitions, as in the
-- event calibration: attempts are the effective ones; teams_tried are the
-- (team, event task) pairs with an attempt; teams_engaged add the pairs that
-- only opened the task; solves come from team_challenge_solves; the solve
-- time is measured from the team's first open or attempt of that task and is
-- pooled over all events (seconds, -1 while nobody solved it); teams_hinted
-- are the engaged pairs that unlocked at least one hint.
WITH evs AS (
    SELECT e.id
    FROM events e
    WHERE e.lifecycle_configured
      AND e.start_at >= sqlc.arg(from_at)::timestamptz AND e.start_at < sqlc.arg(to_at)::timestamptz
), chal AS (
    SELECT ec.id AS challenge_id, ee.event_id, ee.exercise_id, ec.task_id, ex.name AS exercise_name, ex.tags,
           COALESCE(ec.snapshot ->> 'name', '')       AS task_name,
           COALESCE(ec.snapshot ->> 'difficulty', '') AS difficulty
    FROM event_exercises ee
    JOIN exercises ex ON ex.id = ee.exercise_id AND ex.scope = 0
    JOIN event_challenges ec ON ec.event_exercise_id = ee.id AND ec.published
    WHERE ee.event_id IN (SELECT id FROM evs)
      AND ee.status <> 2
      AND (sqlc.narg(category)::text IS NULL OR sqlc.narg(category)::text = ANY (ex.tags))
      AND (sqlc.narg(level)::text IS NULL OR ec.snapshot ->> 'difficulty' = sqlc.narg(level)::text)
), attempts AS (
    SELECT tc.event_challenge_id AS challenge_id, a.event_team_id AS team_id,
           count(*) AS attempts, min(a.received_at) AS first_at
    FROM effective_challenge_attempts a
    JOIN team_challenges tc ON tc.id = a.team_challenge_id
    JOIN event_teams t ON t.id = a.event_team_id AND NOT t.moderators
    WHERE a.event_id IN (SELECT id FROM evs)
      AND tc.event_challenge_id IN (SELECT challenge_id FROM chal)
    GROUP BY tc.event_challenge_id, a.event_team_id
), opens AS (
    SELECT o.subject_id AS challenge_id, o.team_id, min(o.at) AS first_at
    FROM event_activity o
    JOIN event_teams t ON t.id = o.team_id AND NOT t.moderators
    WHERE o.event_id IN (SELECT id FROM evs)
      AND o.kind = 'task_opened'
      AND o.subject_id IN (SELECT challenge_id FROM chal)
    GROUP BY o.subject_id, o.team_id
), engaged AS (
    SELECT challenge_id, team_id FROM attempts
    UNION
    SELECT challenge_id, team_id FROM opens
), solves AS (
    SELECT tc.event_challenge_id AS challenge_id, tc.event_team_id AS team_id, s.solved_at
    FROM team_challenge_solves s
    JOIN team_challenges tc ON tc.id = s.team_challenge_id
    JOIN event_teams t ON t.id = tc.event_team_id AND NOT t.moderators
    WHERE tc.event_id IN (SELECT id FROM evs)
      AND tc.event_challenge_id IN (SELECT challenge_id FROM chal)
), hinted AS (
    SELECT h.event_challenge_id AS challenge_id, h.event_team_id AS team_id
    FROM team_challenge_hint_unlocks h
    WHERE h.event_id IN (SELECT id FROM evs)
      AND h.event_challenge_id IN (SELECT challenge_id FROM chal)
    GROUP BY h.event_challenge_id, h.event_team_id
), attempt_totals AS (
    SELECT challenge_id, sum(attempts) AS attempts, count(*) AS teams_tried
    FROM attempts GROUP BY challenge_id
), engaged_totals AS (
    SELECT challenge_id, count(*) AS teams_engaged FROM engaged GROUP BY challenge_id
), solve_totals AS (
    SELECT challenge_id, count(*) AS solves FROM solves GROUP BY challenge_id
), hint_totals AS (
    SELECT g.challenge_id, count(*) AS teams_hinted
    FROM hinted g
    JOIN engaged e ON e.challenge_id = g.challenge_id AND e.team_id = g.team_id
    GROUP BY g.challenge_id
), solve_times AS (
    SELECT c.exercise_id, c.task_id,
           EXTRACT(EPOCH FROM (s.solved_at - LEAST(a.first_at, o.first_at))) AS secs
    FROM solves s
    JOIN chal c ON c.challenge_id = s.challenge_id
    LEFT JOIN attempts a ON a.challenge_id = s.challenge_id AND a.team_id = s.team_id
    LEFT JOIN opens o ON o.challenge_id = s.challenge_id AND o.team_id = s.team_id
), medians AS (
    SELECT exercise_id, task_id, percentile_cont(0.5) WITHIN GROUP (ORDER BY secs) AS median_secs
    FROM solve_times
    WHERE secs IS NOT NULL AND secs >= 0
    GROUP BY exercise_id, task_id
), tasks AS (
    SELECT c.exercise_id, c.task_id,
           max(c.exercise_name)                              AS exercise_name,
           max(c.task_name)                                  AS task_name,
           max(c.difficulty)                                 AS difficulty,
           c.tags                                            AS tags,
           count(DISTINCT c.event_id)                        AS events_used,
           COALESCE(sum(at.attempts), 0)                     AS attempts,
           COALESCE(sum(at.teams_tried), 0)                  AS teams_tried,
           COALESCE(sum(en.teams_engaged), 0)                AS teams_engaged,
           COALESCE(sum(so.solves), 0)                       AS solves,
           COALESCE(sum(hi.teams_hinted), 0)                 AS teams_hinted
    FROM chal c
    LEFT JOIN attempt_totals at ON at.challenge_id = c.challenge_id
    LEFT JOIN engaged_totals en ON en.challenge_id = c.challenge_id
    LEFT JOIN solve_totals so ON so.challenge_id = c.challenge_id
    LEFT JOIN hint_totals hi ON hi.challenge_id = c.challenge_id
    GROUP BY c.exercise_id, c.task_id, c.tags
)
SELECT t.exercise_id                                  AS exercise_id,
       t.task_id                                      AS task_id,
       t.exercise_name::text                          AS exercise_name,
       t.task_name::text                              AS task_name,
       t.difficulty::text                             AS difficulty,
       t.tags::text[]                                 AS tags,
       t.events_used::bigint                          AS events_used,
       t.attempts::bigint                             AS attempts,
       t.teams_tried::bigint                          AS teams_tried,
       t.teams_engaged::bigint                        AS teams_engaged,
       t.solves::bigint                               AS solves,
       t.teams_hinted::bigint                         AS teams_hinted,
       COALESCE(round(m.median_secs), -1)::bigint     AS median_solve_secs,
       count(*) OVER ()::bigint                       AS total
FROM tasks t
LEFT JOIN medians m ON m.exercise_id = t.exercise_id AND m.task_id = t.task_id
ORDER BY t.events_used DESC, t.exercise_name, t.task_name, t.task_id
LIMIT sqlc.arg(row_limit);

-- name: ListPlatformAnalyticsTaskCategories :many
-- The categories (exercise tags) of the catalog tasks used in the period:
-- the options of the category filter.
SELECT DISTINCT cat.name::text AS category
FROM event_exercises ee
JOIN events e ON e.id = ee.event_id AND e.lifecycle_configured
     AND e.start_at >= sqlc.arg(from_at)::timestamptz AND e.start_at < sqlc.arg(to_at)::timestamptz
JOIN exercises ex ON ex.id = ee.exercise_id AND ex.scope = 0
CROSS JOIN LATERAL unnest(ex.tags) AS cat(name)
WHERE ee.status <> 2
ORDER BY 1
LIMIT 200;
