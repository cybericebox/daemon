create table if not exists event_challenge_solution_attempts
(
    id             uuid primary key,
    event_id       uuid        not null references events (id) on delete cascade,
    challenge_id   uuid        not null references event_challenges (id) on delete cascade,
    team_id        uuid        not null references event_teams (id) on delete cascade,
    participant_id uuid not null references users (id) on delete cascade,

    answer         text        not null,
    flag           text        not null,
    is_correct     boolean     not null,
    timestamp      timestamptz not null
);