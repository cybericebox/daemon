CREATE TABLE event_lab_observations
(
    id             uuid        PRIMARY KEY,
    event_id       uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    event_team_id  uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    lab_group_name text        NOT NULL,
    agent_id       text        NOT NULL,
    sequence       bigint      NOT NULL CHECK (sequence > 0),
    observed_at    timestamptz NOT NULL,
    received_at    timestamptz NOT NULL,
    schema_version integer     NOT NULL CHECK (schema_version > 0),
    snapshot       boolean     NOT NULL,
    payload        jsonb       NOT NULL,
    UNIQUE (agent_id, sequence, lab_group_name)
);

CREATE INDEX event_lab_observations_event_team_time_idx
    ON event_lab_observations (event_id, event_team_id, observed_at DESC, id DESC);
