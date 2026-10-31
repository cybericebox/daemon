-- A team that joined the cohort of a task after the cohort's labs were reserved, and whose labs did not fit the
-- reservation, stays out of the cohort (the others are not held back by it). The need and the room are kept for the
-- organizers.
CREATE TABLE event_lab_reveal_declined
(
    event_exercise_id uuid        NOT NULL REFERENCES event_exercises (id) ON DELETE CASCADE,
    event_team_id     uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    reason            text        NOT NULL,
    declined_at       timestamptz NOT NULL,
    PRIMARY KEY (event_exercise_id, event_team_id)
);
