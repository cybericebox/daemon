create table if not exists event_team_challenges
(
    id           uuid primary key,
    event_id     uuid        not null references events (id) on delete cascade,
    team_id      uuid        not null references event_teams (id) on delete cascade,
    challenge_id uuid        not null references event_challenges (id) on delete cascade,

    flag         text        not null,

    updated_at   timestamptz,
    updated_by   uuid        references users (id) on delete set null,

    created_at   timestamptz not null default now()
);

create unique index if not exists event_team_challenge_index on event_team_challenges (event_id, team_id, challenge_id); -- for checking the team has the challenge in the event