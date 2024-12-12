-- name: CreateEventChallengeSolutionAttempt :exec
insert into event_challenge_solution_attempts
(id, event_id, challenge_id, team_id, participant_id, answer, flag, is_correct, timestamp)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9);

-- name: GetChallengesSolutionsInEvent :many
select challenge_id, t.id as team_id, t.hidden, participant_id, timestamp
from event_challenge_solution_attempts
         inner join event_teams t on t.id = event_challenge_solution_attempts.team_id
where is_correct = true
  and event_challenge_solution_attempts.event_id = $1
  and timestamp between @from_time::timestamptz and @to_time::timestamptz
order by timestamp;

-- name: GetTeamsChallengeSolvedByInEvent :many
select t.id, t.name, t.hidden, participant_id, timestamp
from event_challenge_solution_attempts
         inner join event_teams t on t.id = event_challenge_solution_attempts.team_id
where is_correct = true
  and challenge_id = $2
  and event_challenge_solution_attempts.event_id = $1
order by timestamp;

-- name: GetTeamsChallengeSolvedByInEventPaged :many
select t.id, t.name, t.hidden, participant_id, timestamp
from event_challenge_solution_attempts
         inner join event_teams t on t.id = event_challenge_solution_attempts.team_id
where is_correct = true
  and challenge_id = $2
  and event_challenge_solution_attempts.event_id = $1
order by timestamp
limit $3 offset $4;


-- name: GetEventChallengeSolutionAttemptsPaged :many
select sa.id,
       sa.event_id,
       challenge_id,
       team_id,
       t.name as team_name,
       participant_id,
       u.name as participant_name,
       answer,
       flag,
       is_correct,
       timestamp
from event_challenge_solution_attempts sa
         inner join event_teams t on t.id = sa.team_id
         inner join users u on u.id = sa.participant_id
where sa.event_id = $1
order by timestamp desc
limit $2 offset $3;

-- name: UpdateEventChallengeSolutionAttempt :execrows
update event_challenge_solution_attempts
set is_correct = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

