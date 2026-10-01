CREATE TABLE lab_bindings
(
    id                uuid        PRIMARY KEY,
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    event_team_id     uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    event_exercise_id uuid        NOT NULL REFERENCES event_exercises (id) ON DELETE RESTRICT,
    lab_group_name    text        NOT NULL,
    lab_name          text        NOT NULL,
    created_at        timestamptz NOT NULL,
    UNIQUE (event_team_id, event_exercise_id),
    UNIQUE (lab_group_name, lab_name)
);
