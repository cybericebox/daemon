-- name: CreateEventTeamChallenge :batchexec
insert into event_team_challenges
    (id, event_id, team_id, challenge_id, flag)
values ($1, $2, $3, $4, $5)
on conflict do nothing;

-- name: GetChallengeFlag :one
select flag
from event_team_challenges
where challenge_id = $1
  and team_id = $2;