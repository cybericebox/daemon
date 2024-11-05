-- name: GetEventParticipants :many
select event_participants.*, u.name as name, u.email as email
from event_participants
         inner join users u on event_participants.user_id = u.id
where event_id = $1
order by event_participants.created_at desc;

-- name: CreateEventParticipant :exec
insert into event_participants (event_id, user_id, approval_status)
values ($1, $2, $3);

-- name: GetEventParticipantStatus :one
select approval_status
from event_participants
where event_id = $1
  and user_id = $2;

-- name: UpdateEventParticipantStatus :execrows
update event_participants
set approval_status = $3,
    updated_at      = now(),
    updated_by      = $4
where event_id = $1
  and user_id = $2;

-- name: UpdateEventParticipantTeam :execrows
update event_participants
set team_id    = $3,
    updated_at = now(),
    updated_by = $4
where event_id = $1
  and user_id = $2;

-- name: DeleteEventParticipant :execrows
delete
from event_participants
where event_id = $1
  and user_id = $2;