-- name: GetEvents :many
select *
from events;

-- name: GetEventsPaged :many
select *
from events
order by name
limit $1 offset $2;

-- name: GetEventByID :one
select *
from events
where id = $1;

-- name: GetEventByTag :one
select *
from events
where tag = $1;

-- name: CreateEvent :exec
insert into events (id, type, availability, participation, tag, name, dynamic_scoring,
                    dynamic_max, dynamic_min, dynamic_solve_threshold, registration, scoreboard_availability,
                    participants_visibility, publish_time, start_time, finish_time, withdraw_time)
values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17);

-- name: UpdateEvent :execrows
update events
set type                    = $2,
    availability            = $3,
    name                    = $4,
    dynamic_scoring         = $5,
    dynamic_max             = $6,
    dynamic_min             = $7,
    dynamic_solve_threshold = $8,
    registration            = $9,
    scoreboard_availability = $10,
    participants_visibility = $11,
    publish_time            = $12,
    start_time              = $13,
    finish_time             = $14,
    withdraw_time           = $15,
    updated_by = $16,
    updated_at = now()
where id = $1;

-- name: DeleteEvent :execrows
delete
from events
where id = $1;

-- name: GetEventsWithMetadata :many
select events.*,
       events_metadata.data
from events
         inner join events_metadata on events.id = events_metadata.event_id
order by events.name;

-- name: GetEventsWithMetadataPaged :many
select events.*,
       events_metadata.data
from events
         inner join events_metadata on events.id = events_metadata.event_id
order by events.name
limit $1 offset $2;

-- name: GetEventWithMetadataByID :one
select events.*,
       events_metadata.data
from events_metadata
         inner join events on events.id = events_metadata.event_id
where events_metadata.event_id = $1;

-- name: GetEventsMetadata :many
select event_id, data
from events_metadata;

-- name: CreateEventMetadata :exec
insert into events_metadata (event_id, data)
values ($1, $2);

-- name: UpdateEventMetadata :execrows
update events_metadata
set data = $2
where event_id = $1;

-- name: UpdateEventPicture :execrows
update events_metadata
set data = jsonb_set(data, '{Picture}', to_jsonb(@picture::text))
where event_id = $1;
