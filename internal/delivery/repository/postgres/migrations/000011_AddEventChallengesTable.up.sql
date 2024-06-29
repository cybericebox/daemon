create table if not exists event_challenges
(
    id               uuid primary key,
    event_id         uuid         not null references events (id) on delete cascade,
    category_id      uuid         not null references event_challenge_categories (id) on delete cascade,

    name             varchar(255) not null,
    description      text         not null,
    points           integer      not null,
    order_index      integer      not null,

    -- Reference to the exercise and subtask
    exercise_id      uuid         not null references exercises (id) on delete no action,
    exercise_task_id uuid         not null,

    updated_at       timestamptz,
    updated_by       uuid         references users (id) on delete set null,

    created_at       timestamptz  not null default now()
);

create unique index if not exists event_challenge_index on event_challenges (event_id, name); -- for checking the challenge name is unique in the event
