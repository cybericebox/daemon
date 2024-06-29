-- name: CreateEventParticipant :exec
insert into event_participants (event_id, user_id, approval_status)
values ($1, $2, $3);

-- name: GetEventJoinStatus :one
select approval_status
from event_participants
where event_id = $1
  and user_id = $2;

-- name: UpdateEventParticipantStatus :exec
update event_participants
set approval_status = $3
where event_id = $1
  and user_id = $2;

-- name: UpdateEventParticipantTeam :exec
update event_participants
set team_id = $3
where event_id = $1
  and user_id = $2;