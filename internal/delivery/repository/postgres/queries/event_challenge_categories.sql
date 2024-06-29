-- name: GetEventChallengeCategories :many
select *
from event_challenge_categories
where event_id = $1
order by order_index;

-- name: CreateEventChallengeCategory :exec
insert into event_challenge_categories
    (id, event_id, name, order_index)
values ($1, $2, $3, $4);

-- name: UpdateEventChallengeCategory :exec
update event_challenge_categories
set name = $3
where id = $1
  and event_id = $2;

-- name: UpdateEventChallengeCategoryOrder :exec
update event_challenge_categories
set order_index = $3
where id = $1
  and event_id = $2;

-- name: DeleteEventChallengeCategory :exec
delete
from event_challenge_categories
where id = $1
  and event_id = $2;