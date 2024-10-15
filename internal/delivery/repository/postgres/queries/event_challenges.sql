-- name: CountChallengesInEvents :many
select count(*), event_id
from event_challenges
group by event_id;

-- name: CountChallengesInCategoryInEvent :one
select count(*)
from event_challenges
where category_id = $1
  and event_id = $2;

-- name: GetEventChallenges :many
select *
from event_challenges
where event_id = $1
order by order_index;

-- name: GetEventChallengeByID :one
select *
from event_challenges
where id = $1
  and event_id = $2;

-- name: CreateEventChallenge :batchexec
insert into event_challenges
(id, event_id, category_id, data, order_index, exercise_id, exercise_task_id)
values ($1, $2, $3, $4, $5, $6, $7);

-- name: UpdateEventChallengeOrder :batchexec
update event_challenges
set category_id = $3,
    order_index = $4,
    updated_at  = now(),
    updated_by  = $5
where id = $1
  and event_id = $2;

-- name: DeleteEventChallenges :batchexec
delete
from event_challenges
where exercise_id = $1
  and event_id = $2;