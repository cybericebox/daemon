-- name: GetEventParticipants :many
select *
from event_participants
where event_id = $1;

-- name: CreateEventParticipant :exec
insert into event_participants (event_id, user_id, name, approval_status)
values ($1, $2, $3, $4);

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

-- name: UpdateEventParticipantName :execrows
update event_participants
set name       = $3,
    updated_at = now(),
    updated_by = $4
where event_id = $1
  and user_id = $2;

-- name: DeleteEventParticipant :execrows
delete
from event_participants
where event_id = $1
  and user_id = $2;