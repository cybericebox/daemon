-- name: CountTeamsInEvents :many
select count(*), event_id
from event_teams
group by event_id;

-- name: GetEventTeams :many
select id, event_id, name, laboratory_id, updated_at, updated_by, created_at
from event_teams
where event_id = $1;

-- name: TeamExistsInEvent :one
select EXISTS(select true as exists from event_teams where name = $1 and event_id = $2) as exists;

-- name: CreateTeamInEvent :exec
insert into event_teams (id, name, join_code, event_id, laboratory_id)
values ($1, $2, $3, $4, $5);


-- name: GetEventTeamByName :one
select id, name, join_code
from event_teams
where name = $1
  and event_id = $2;

-- name: GetEventParticipantTeam :one
select event_teams.id, name, join_code, laboratory_id
from event_teams
         join event_participants on event_teams.id = event_participants.team_id
where event_participants.event_id = $1
  and event_participants.user_id = $2;

-- name: GetEventParticipantTeamID :one
select team_id
from event_participants
where event_id = $1
  and user_id = $2;


