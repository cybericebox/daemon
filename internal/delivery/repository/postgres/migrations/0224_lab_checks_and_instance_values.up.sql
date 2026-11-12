-- What the daemon keeps for the laboratory contract 1.5 (device bootstrap, lab checks, service command).
--
--   platform_image_configs  the start configuration (entrypoint, cmd, user, working directory) of an image of the platform
--                           registry, read from its config blob when the image is published; the laboratory is told what to run
--                           after a bootstrap from it. One row per digest.
--   stand_variables         a variable of the infrastructure drawn once for a team's stand of an event exercise, so a rebuilt or
--                           reset lab gets the same values (a test lab is built once and has none to keep).
--   lab_check_results       the last result of every laboratory check of a lab, by the lab's name (a stand lab or a test lab).
--                           first_passed_at never goes back: a check of mode once stays passed. detail is for staff only.
--   lab_check_runs          the manual runs of checks that were asked for, for the rate limit per lab group and task.
CREATE TABLE platform_image_configs
(
    digest     text        PRIMARY KEY,
    service    jsonb       NOT NULL,
    created_at timestamptz NOT NULL
);

CREATE TABLE stand_variables
(
    event_team_id     uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    event_exercise_id uuid        NOT NULL REFERENCES event_exercises (id) ON DELETE CASCADE,
    name              text        NOT NULL,
    value             text        NOT NULL,
    created_at        timestamptz NOT NULL,
    PRIMARY KEY (event_team_id, event_exercise_id, name)
);

CREATE TABLE lab_check_results
(
    lab_group_name  text        NOT NULL,
    lab_name        text        NOT NULL,
    check_id        text        NOT NULL,
    state           smallint    NOT NULL, -- 1 passed, 2 not passed
    first_passed_at timestamptz NULL,
    changed_at      timestamptz NOT NULL,
    value           text        NOT NULL DEFAULT '',
    detail          text        NOT NULL DEFAULT '',
    observed_at     timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    PRIMARY KEY (lab_group_name, lab_name, check_id),
    CONSTRAINT lab_check_results_state_check CHECK (state IN (1, 2))
);
CREATE INDEX lab_check_results_updated_idx ON lab_check_results (updated_at);

CREATE TABLE lab_check_runs
(
    id             bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    lab_group_name text        NOT NULL,
    task_key       text        NOT NULL,
    asked_at       timestamptz NOT NULL
);
CREATE INDEX lab_check_runs_group_task_idx ON lab_check_runs (lab_group_name, task_key, asked_at DESC);
