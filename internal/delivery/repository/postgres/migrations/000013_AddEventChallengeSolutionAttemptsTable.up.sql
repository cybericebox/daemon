create table if not exists event_challenge_solution_attempts
(
    id             uuid primary key,
    challenge_id   uuid        not null references event_challenges (id) on delete cascade,
    team_id        uuid        not null references event_teams (id) on delete cascade,
    participant_id uuid        not null references users (id) on delete cascade,

    answer         text        not null,
    flag           text        not null,
    is_correct     boolean     not null,
    timestamp      timestamptz not null,

    updated_at     timestamptz,
    updated_by     uuid        references users (id) on delete set null
);

-- unique index for checking the user has only one correct attempt for the challenge and multiple incorrect attempts
create unique index if not exists event_challenge_solution_attempt_correct_index on event_challenge_solution_attempts (is_correct, team_id, challenge_id) where is_correct = true;