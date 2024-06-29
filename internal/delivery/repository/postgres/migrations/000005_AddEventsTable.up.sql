create table if not exists events
(
    id                      uuid primary key,

    type                    integer      not null, -- 0: competition, 1: practice
    availability            integer      not null, -- 0: public, 1: private
    participation           integer      not null, -- 0: individual, 1: team

    tag                     varchar(64)  not null, -- subdomain for the event
    name                    varchar(255) not null,
    description             text         not null, -- description of the event
    rules                   text         not null, -- rules for the event
    picture                 text         not null,

    dynamic_scoring         boolean      not null,
    dynamic_max             integer      not null,
    dynamic_min             integer      not null,
    dynamic_solve_threshold integer      not null,

    registration            integer      not null, -- 0: open, 1: approval, 2: close

    scoreboard_availability integer      not null, -- 0: none, 1: public, 2: private
    participants_visibility integer      not null, -- 0: none, 1: public, 2: private

    publish_time            timestamptz  not null, -- when the event is published
    start_time              timestamptz  not null, -- when the event starts
    finish_time             timestamptz  not null, -- when the event finishes
    withdraw_time           timestamptz  not null, -- when the event withdraws

    updated_at              timestamptz,
    updated_by              uuid         references users (id) on delete set null,

    created_at              timestamptz  not null default now()
);

create unique index if not exists event_tag_index on events (tag, withdraw_time); -- for checking the tag is unique