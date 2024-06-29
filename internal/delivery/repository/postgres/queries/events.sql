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

-- name: GetAllEvents :many
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

-- name: UpdateEvent :exec
update events
set name                    = $2,
    description             = $3,
    rules                   = $4,
    picture                 = $5,
    dynamic_scoring         = $6,
    dynamic_max             = $7,
    dynamic_min             = $8,
    dynamic_solve_threshold = $9,
    registration            = $10,
    scoreboard_availability = $11,
    participants_visibility = $12,
    publish_time            = $13,
    start_time              = $14,
    finish_time             = $15,
    withdraw_time           = $16
where id = $1;

-- name: DeleteEvent :exec
delete
from events
where id = $1;
