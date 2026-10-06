-- Event analytics, «Звіт по заходу» (docs/EVENT-ANALYTICS.md §6.8): the
-- ranking, the per-task table and the funnel tail. The key numbers and the
-- activity series come from the overview statements.

-- name: ListEventReportRanking :many
-- The final table of the ranked teams (admitted, not hidden), live scores.
SELECT t.id                                                                    AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       t.individual,
       t.member_count::bigint                                                  AS member_count,
       COALESCE(SUM(score.points), 0)::bigint                                  AS points,
       count(score.team_challenge_id) FILTER (WHERE score.solve)::bigint       AS solved,
       COALESCE(extract(epoch FROM MAX(score.solved_at) FILTER (WHERE score.solve)), 0)::bigint AS last_solve_unix,
       (SELECT count(*) FROM effective_challenge_attempts a
        WHERE a.event_team_id = t.id)::bigint                                  AS attempts
FROM event_teams t
LEFT JOIN event_solved_scores_at(sqlc.arg(event_id)::uuid, NULL, NULL) score ON score.event_team_id = t.id
WHERE t.event_id = sqlc.arg(event_id)
  AND NOT t.moderators
  AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
GROUP BY t.id
ORDER BY points DESC, MAX(score.solved_at) FILTER (WHERE score.solve) ASC NULLS LAST, t.id ASC;

-- name: ListEventReportTasks :many
-- Per-task statistics over all teams but the moderators team, in board order.
WITH teams AS (
    SELECT t.id, event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS name
    FROM event_teams t
    WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators
), att AS (
    SELECT tc.event_challenge_id AS challenge_id, a.event_team_id AS team_id,
           count(*) AS attempts, count(*) FILTER (WHERE a.effective_correct) AS correct
    FROM effective_challenge_attempts a
    JOIN teams ON teams.id = a.event_team_id
    JOIN team_challenges tc ON tc.id = a.team_challenge_id
    WHERE a.event_id = sqlc.arg(event_id)
    GROUP BY 1, 2
), sol AS (
    SELECT tc.event_challenge_id AS challenge_id, teams.name AS team_name, s.solved_at
    FROM team_challenge_solves s
    JOIN team_challenges tc ON tc.id = s.team_challenge_id
    JOIN teams ON teams.id = tc.event_team_id
    WHERE tc.event_id = sqlc.arg(event_id)
), opens AS (
    SELECT o.subject_id AS challenge_id, count(DISTINCT o.team_id) AS teams
    FROM event_activity o
    JOIN teams ON teams.id = o.team_id
    WHERE o.event_id = sqlc.arg(event_id) AND o.kind = 'task_opened' AND o.subject_id IS NOT NULL
    GROUP BY 1
), hints AS (
    SELECT h.event_challenge_id AS challenge_id, count(*) AS unlocked
    FROM team_challenge_hint_unlocks h
    JOIN teams ON teams.id = h.event_team_id
    WHERE h.event_id = sqlc.arg(event_id)
    GROUP BY 1
)
SELECT c.id                                                           AS challenge_id,
       COALESCE(c.snapshot ->> 'name', '')::text                      AS name,
       c.points::bigint                                               AS points,
       COALESCE((SELECT o.teams FROM opens o WHERE o.challenge_id = c.id), 0)::bigint AS teams_opened,
       (SELECT count(*) FROM att WHERE att.challenge_id = c.id)::bigint               AS teams_attempted,
       COALESCE((SELECT sum(att.attempts) FROM att WHERE att.challenge_id = c.id), 0)::bigint AS attempts,
       COALESCE((SELECT sum(att.correct) FROM att WHERE att.challenge_id = c.id), 0)::bigint  AS correct_attempts,
       (SELECT count(*) FROM sol WHERE sol.challenge_id = c.id)::bigint               AS solves,
       COALESCE((SELECT h.unlocked FROM hints h WHERE h.challenge_id = c.id), 0)::bigint AS hints_unlocked,
       COALESCE((SELECT extract(epoch FROM min(sol.solved_at)) FROM sol WHERE sol.challenge_id = c.id), 0)::bigint AS first_solve_unix,
       COALESCE((SELECT sol.team_name FROM sol WHERE sol.challenge_id = c.id
                 ORDER BY sol.solved_at LIMIT 1), '')::text                              AS first_solve_team
FROM event_challenges c
JOIN event_exercises ex ON ex.id = c.event_exercise_id
WHERE ex.event_id = sqlc.arg(event_id)
  AND c.published
ORDER BY ex.created_at, c.order_index, c.id;

-- name: GetEventReportFunnel :one
-- The participation funnel below the approved participants: how many of
-- them and their teams ever opened a task, tried an answer and solved.
WITH teams AS (
    SELECT t.id FROM event_teams t WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators
)
SELECT (SELECT count(DISTINCT o.user_id) FROM event_activity o JOIN teams ON teams.id = o.team_id
        WHERE o.event_id = sqlc.arg(event_id) AND o.kind = 'task_opened' AND o.user_id IS NOT NULL)::bigint AS participants_opened,
       (SELECT count(DISTINCT a.user_id) FROM effective_challenge_attempts a JOIN teams ON teams.id = a.event_team_id
        WHERE a.event_id = sqlc.arg(event_id))::bigint                                                    AS participants_attempted,
       (SELECT count(DISTINCT a.event_team_id) FROM effective_challenge_attempts a JOIN teams ON teams.id = a.event_team_id
        WHERE a.event_id = sqlc.arg(event_id))::bigint                                                    AS teams_attempted,
       (SELECT count(DISTINCT tc.event_team_id) FROM team_challenge_solves s
        JOIN team_challenges tc ON tc.id = s.team_challenge_id JOIN teams ON teams.id = tc.event_team_id
        WHERE tc.event_id = sqlc.arg(event_id))::bigint                                                    AS teams_solved;
