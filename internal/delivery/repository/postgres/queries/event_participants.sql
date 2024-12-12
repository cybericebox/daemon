-- name: GetEventParticipants :many
select event_participants.*, u.name as name, u.email as email, coalesce(et.name, '') as team_name
from event_participants
         inner join users u on event_participants.user_id = u.id
         left join public.event_teams et on event_participants.team_id = et.id
where event_participants.event_id = $1;

-- name: GetEventParticipantsPaged :many
select event_participants.*, u.name as name, u.email as email, coalesce(et.name, '') as team_name
from event_participants
         inner join users u on event_participants.user_id = u.id
         left join public.event_teams et on event_participants.team_id = et.id
where event_participants.event_id = $1
order by event_participants.created_at desc
limit $2 offset $3;

-- name: CreateEventParticipant :exec
insert into event_participants (event_id, user_id, approval_status)
values ($1, $2, $3);

-- name: GetEventParticipantStatus :one
select approval_status
from event_participants
where user_id = $1
  and event_id = $2;

-- name: UpdateEventParticipantStatus :execrows
update event_participants
set approval_status = $3,
    updated_at      = now(),
    updated_by      = $4
where user_id = $1
  and event_id = $2;

-- name: UpdateEventParticipantTeam :execrows
update event_participants
set team_id    = $3,
    updated_at = now(),
    updated_by = $4
where user_id = $1
  and event_id = $2;

-- name: DeleteEventParticipant :execrows
delete
from event_participants
where user_id = $1
  and event_id = $2;