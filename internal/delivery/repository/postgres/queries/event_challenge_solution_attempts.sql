-- -- name: GetAllSolvedChallengesIDsByTeamInEvent :many
-- select challenge_id
-- from event_challenge_solution_attempts
-- where event_id = $1
--   and team_id = $2
--   and is_correct = true;

-- name: GetAllChallengesSolutionsInEvent :many
select challenge_id, team_id, participant_id, timestamp
from event_challenge_solution_attempts
where event_id = $1
  and is_correct = true;

-- name: GetTeamsSolvedChallengeInEvent :many
select t.id, t.name, participant_id, timestamp
from event_challenge_solution_attempts
         inner join event_teams t on t.id = event_challenge_solution_attempts.team_id
where t.event_id = $1
  and challenge_id = $2
  and is_correct = true;

-- name: CreateEventChallengeSolutionAttempt :exec
insert into event_challenge_solution_attempts
(id, event_id, challenge_id, team_id, participant_id, answer, flag, is_correct, timestamp)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9);
