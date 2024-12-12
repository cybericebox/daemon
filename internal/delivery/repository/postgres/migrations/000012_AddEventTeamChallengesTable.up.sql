create table if not exists event_team_challenges
(
    team_id      uuid        not null references event_teams (id) on delete cascade,
    challenge_id uuid        not null references event_challenges (id) on delete cascade,

    flag         text        not null,

    updated_at   timestamptz,
    updated_by   uuid        references users (id) on delete set null,

    created_at timestamptz not null default now(),

    primary key (team_id, challenge_id)
);