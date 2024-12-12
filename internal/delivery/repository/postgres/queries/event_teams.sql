-- name: CountTeamsInEvents :many
select count(*), event_id
from event_teams
group by event_id;

-- name: GetEventTeams :many
select id,
       event_teams.event_id,
       event_teams.name,
       laboratory_id,
       event_teams.hidden,
       event_teams.approval_status,
       event_teams.updated_at,
       event_teams.updated_by,
       event_teams.created_at,
       count(event_participants.user_id) as participants_count
from event_teams
         left join event_participants on event_teams.id = event_participants.team_id
where event_teams.event_id = $1
group by event_teams.id;

-- name: GetEventTeamsPaged :many
select id,
       event_teams.event_id,
       text(event_teams.name)            as name,
       laboratory_id,
       event_teams.hidden,
       event_teams.approval_status,
       event_teams.updated_at,
       event_teams.updated_by,
       event_teams.created_at,
       count(event_participants.user_id) as participants_count
from event_teams
         left join event_participants on event_teams.id = event_participants.team_id
where event_teams.event_id = $1
group by event_teams.id
order by name
limit $2 offset $3;

-- name: GetEventTeamByID :one
select id,
       event_id,
       name,
       laboratory_id,
       hidden,
       approval_status,
       updated_at,
       updated_by,
       created_at
from event_teams
where id = $1;

-- name: CreateTeamInEvent :exec
insert into event_teams (id, name, join_code, event_id, laboratory_id, hidden, approval_status)
values ($1, $2, $3, $4, $5, $6, $7);

-- name: GetEventTeamByName :one
select id, name, join_code
from event_teams
where name = $1
  and event_id = $2;

-- name: GetEventParticipantTeam :one
select event_teams.id,
       event_teams.name,
       event_teams.join_code,
       event_teams.laboratory_id,
       event_teams.hidden,
       event_teams.approval_status
from event_teams
         inner join event_participants on event_teams.id = event_participants.team_id
where event_participants.user_id = $1
  and event_participants.event_id = $2;

-- name: UpdateEventTeamName :execrows
update event_teams
set name       = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateEventTeamsVisibility :batchexec
update event_teams
set hidden     = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateEventTeamsLaboratories :batchexec
update event_teams
set laboratory_id = $2,
    updated_at    = now(),
    updated_by    = $3
where id = $1;

-- name: UpdateEventTeamApprovalStatus :execrows
update event_teams
set approval_status = $2,
    updated_at      = now(),
    updated_by      = $3
where id = $1;

-- name: DeleteEventTeam :execrows
delete
from event_teams
where id = $1;


