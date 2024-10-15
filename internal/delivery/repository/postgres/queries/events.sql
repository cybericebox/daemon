-- name: GetEventIDIfRunning :one
select id
from events
where tag = $1
  and now() between publish_time and withdraw_time;

-- name: GetEventIDIfNotWithdrawn :one
select id
from events
where tag = $1
  and now() < withdraw_time;

-- name: GetEvents :many
select *
from events;

-- name: GetEventByID :one
select *
from events
where id = $1;

-- name: GetEventByTag :one
select *
from events
where tag = $1;

-- name: CreateEvent :exec
insert into events (id, type, availability, participation, tag, name, description, rules, picture, dynamic_scoring,
                    dynamic_max, dynamic_min, dynamic_solve_threshold, registration, scoreboard_availability,
                    participants_visibility, publish_time, start_time, finish_time, withdraw_time)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20);

-- name: UpdateEvent :execrows
update events
set type                    = $2,
    availability            = $3,
    name                    = $4,
    description             = $5,
    rules                   = $6,
    picture                 = $7,
    dynamic_scoring         = $8,
    dynamic_max             = $9,
    dynamic_min             = $10,
    dynamic_solve_threshold = $11,
    registration            = $12,
    scoreboard_availability = $13,
    participants_visibility = $14,
    publish_time            = $15,
    start_time              = $16,
    finish_time             = $17,
    withdraw_time           = $18,
    updated_at              = now(),
    updated_by              = $19
where id = $1;

-- name: UpdateEventPicture :execrows
update events
set picture = $2
where id = $1;

-- name: DeleteEvent :execrows
delete
from events
where id = $1;
