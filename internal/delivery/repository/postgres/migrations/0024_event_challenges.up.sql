CREATE TABLE event_challenges
(
    id                uuid        PRIMARY KEY,
    event_exercise_id uuid        NOT NULL REFERENCES event_exercises (id) ON DELETE CASCADE,
    task_id           uuid        NOT NULL,
    order_index       integer     NOT NULL,
    points            integer     NOT NULL CHECK (points > 0),
    hints_enabled     boolean     NOT NULL DEFAULT false,
    published         boolean     NOT NULL DEFAULT false,
    snapshot          jsonb       NOT NULL,
    created_at        timestamptz NOT NULL,
    UNIQUE (event_exercise_id, task_id),
    UNIQUE (event_exercise_id, order_index)
);

CREATE INDEX event_challenges_event_exercise_order_idx ON event_challenges (event_exercise_id, order_index);
