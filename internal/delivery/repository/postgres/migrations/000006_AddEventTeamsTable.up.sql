create table if not exists event_teams
(
    id            uuid primary key,
    event_id      uuid         not null references events (id) on delete cascade,

    name          varchar(255) not null,
    join_code     varchar(255) not null,

    -- Laboratory
    laboratory_id uuid,

    updated_at    timestamptz,
    updated_by    uuid         references users (id) on delete set null,

    created_at    timestamptz  not null default now()
);

create unique index if not exists event_team_name_index on event_teams (event_id, name); -- for checking the team name is unique in the event

