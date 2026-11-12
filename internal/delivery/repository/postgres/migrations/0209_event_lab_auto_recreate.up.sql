-- How many times the stand engine has recreated the failed Lab of a team's exercise by itself (a Lab the agent accepted
-- that crashed, failed or was not ready in time). Reset when the Lab becomes ready or the organizer recreates it.
CREATE TABLE event_lab_auto_recreate
(
    event_team_id     uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    event_exercise_id uuid        NOT NULL REFERENCES event_exercises (id) ON DELETE CASCADE,
    attempts          integer     NOT NULL,
    last_reason       text        NOT NULL DEFAULT '',
    updated_at        timestamptz NOT NULL,
    PRIMARY KEY (event_team_id, event_exercise_id)
);
