-- name: GetEventChallengeCategories :many
select *
from event_challenge_categories
where event_id = $1
order by order_index;

-- name: GetEventChallengeCategoryByID :one
select *
from event_challenge_categories
where id = $1;

-- name: CreateEventChallengeCategory :exec
insert into event_challenge_categories
    (id, event_id, name, order_index)
values ($1, $2, $3, $4);

-- name: UpdateEventChallengeCategory :execrows
update event_challenge_categories
set name       = $2,
    updated_at = now(),
    updated_by = $3
where id = $1;

-- name: UpdateEventChallengeCategoryOrder :batchexec
update event_challenge_categories
set order_index = $2,
    updated_at  = now(),
    updated_by  = $3
where id = $1;

-- name: DeleteEventChallengeCategory :execrows
delete
from event_challenge_categories
where id = $1;