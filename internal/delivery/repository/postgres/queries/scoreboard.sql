-- name: ListEventScoreboard :many
-- Ranked public teams. A NULL cutoff reads live scores; a cutoff reads the
-- table as of that moment (freeze), plus every solve of include_team.
SELECT t.id AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       COALESCE(SUM(score.points), 0)::bigint AS points,
       count(score.team_challenge_id) FILTER (WHERE score.solve)::bigint AS solved,
       MAX(score.solved_at) FILTER (WHERE score.solve) AS last_solve_at
FROM event_teams t
LEFT JOIN event_solved_scores_at(sqlc.arg(event_id)::uuid, sqlc.narg(cutoff)::timestamptz, sqlc.narg(include_team)::uuid) score
       ON score.event_team_id = t.id
WHERE t.event_id = sqlc.arg(event_id) AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
GROUP BY t.id
ORDER BY points DESC, last_solve_at ASC NULLS LAST, t.id ASC;

-- name: ListEventScoreTimeline :many
-- Same cutoff rules as ListEventScoreboard.
SELECT score.event_team_id::uuid AS event_team_id, score.event_challenge_id::uuid AS event_challenge_id,
       COALESCE(challenge.snapshot->>'name', '')::text AS challenge_name,
       score.points::integer AS points, score.solved_at::timestamptz AS solved_at,
       score.team_challenge_id::uuid AS team_challenge_id
FROM event_solved_scores_at(sqlc.arg(event_id)::uuid, sqlc.narg(cutoff)::timestamptz, sqlc.narg(include_team)::uuid) score
JOIN event_teams team ON team.id = score.event_team_id
LEFT JOIN event_challenges challenge ON challenge.id = score.event_challenge_id
WHERE event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually, team.admission_locked, team.member_count)
ORDER BY 5 ASC, 6 ASC;

-- name: ListTeamScoreTimeline :many
-- Includes balance-mode hint penalties (negative points) so the running
-- total matches the scoreboard.
SELECT score.event_challenge_id::uuid AS event_challenge_id, score.points::integer AS points,
       score.solved_at::timestamptz AS solved_at, score.team_challenge_id::uuid AS team_challenge_id
FROM event_teams team
CROSS JOIN LATERAL event_solved_scores_at(team.event_id, NULL, NULL) score
WHERE team.id = sqlc.arg(event_team_id)
  AND score.event_team_id = team.id
ORDER BY 3 ASC, 4 ASC;

-- name: ListManageScoreboard :many
-- Every team of the event, the moderators team included, live, with the marks a
-- moderator needs: hidden, admitted, and for individual events the real name
-- and pseudonym of the participant behind the solo team.
SELECT t.id AS team_id,
       t.individual,
       t.hidden,
       t.moderators,
       event_team_admitted(t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count) AS admitted,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS public_name,
       (CASE WHEN t.individual
                 THEN COALESCE(NULLIF(btrim(concat_ws(' ', person.first_name, person.last_name)), ''), t.name)
             ELSE t.name END)::text AS real_name,
       (CASE WHEN t.individual THEN COALESCE(NULLIF(btrim(participant.pseudonym), ''), '') ELSE '' END)::text AS pseudonym,
       COALESCE(SUM(score.points), 0)::bigint AS points,
       count(score.team_challenge_id) FILTER (WHERE score.solve)::bigint AS solved,
       MAX(score.solved_at) FILTER (WHERE score.solve) AS last_solve_at
FROM event_teams t
LEFT JOIN users person ON person.id = t.captain_id
LEFT JOIN event_participants participant ON participant.event_id = t.event_id AND participant.user_id = t.captain_id
LEFT JOIN event_solved_scores_at(sqlc.arg(event_id)::uuid, NULL, NULL) score ON score.event_team_id = t.id
WHERE t.event_id = sqlc.arg(event_id)::uuid
GROUP BY t.id, person.first_name, person.last_name, participant.pseudonym
ORDER BY points DESC, last_solve_at ASC NULLS LAST, t.id ASC;

-- name: ListManageScoreSolves :many
-- The solves behind the moderators' table, live, for every team, the
-- moderators team included. first_blood marks the first solve of a challenge among the
-- ranked teams (not hidden, admitted).
SELECT ranked.event_team_id, ranked.event_challenge_id,
       COALESCE(challenge.snapshot->>'name', '')::text AS challenge_name,
       ranked.points, ranked.solved_at,
       (ranked.counted AND ranked.place = 1)::boolean AS first_blood
FROM (
    SELECT s.event_team_id::uuid AS event_team_id, s.event_challenge_id::uuid AS event_challenge_id,
           s.team_challenge_id::uuid AS team_challenge_id,
           s.points::integer AS points, s.solved_at::timestamptz AS solved_at,
           (event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually, team.admission_locked, team.member_count))::boolean AS counted,
           row_number() OVER (
               PARTITION BY s.event_challenge_id,
                   (event_team_visible(team.hidden, team.moderators, team.event_id, team.individual, team.admitted_manually, team.admission_locked, team.member_count))
               ORDER BY s.solved_at, s.team_challenge_id) AS place
    FROM event_solved_scores_at(sqlc.arg(event_id)::uuid, NULL, NULL) s
    JOIN event_teams team ON team.id = s.event_team_id
    WHERE s.solve
) ranked
LEFT JOIN event_challenges challenge ON challenge.id = ranked.event_challenge_id
ORDER BY 5 ASC, ranked.team_challenge_id ASC;

-- name: ListManageHintTotals :many
-- Hints opened per team and the points they cost. Without the balance mode a
-- hint is charged only from the reward of a later solve of its challenge.
SELECT unlock.event_team_id::uuid AS event_team_id,
       count(*)::bigint AS hints,
       COALESCE(sum(unlock.cost) FILTER (
           WHERE COALESCE(config.hint_charge_mode, 0) = 1
              OR solved.solved_at >= unlock.unlocked_at), 0)::bigint AS charged
FROM team_challenge_hint_unlocks unlock
JOIN event_challenges ec ON ec.id = unlock.event_challenge_id
JOIN event_exercises ee ON ee.id = ec.event_exercise_id AND ee.status <> 2
LEFT JOIN event_configs config ON config.event_id = unlock.event_id
LEFT JOIN team_challenge_solves solved ON solved.team_challenge_id = unlock.team_challenge_id
WHERE unlock.event_id = sqlc.arg(event_id)::uuid
GROUP BY unlock.event_team_id;
