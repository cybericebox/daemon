create table if not exists event_participants
(
    user_id         uuid        not null references users (id) on delete cascade,
    event_id        uuid        not null references events (id) on delete cascade,
    team_id         uuid        references event_teams (id) on delete set null,

    -- Hidden teams are not shown in the list of teams
    hidden          boolean     not null default false,

    approval_status integer     not null default 0, -- 0: pending, 1: approved, 2: rejected

    updated_at      timestamptz,
    updated_by      uuid        references users (id) on delete set null,

    created_at      timestamptz not null default now(),

    primary key (user_id, event_id)
);

