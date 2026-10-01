CREATE TABLE platform_lab_capacity_observations
(
    id             uuid        PRIMARY KEY,
    agent_id       text        NOT NULL,
    sequence       bigint      NOT NULL CHECK (sequence > 0),
    observed_at    timestamptz NOT NULL,
    received_at    timestamptz NOT NULL,
    schema_version integer     NOT NULL CHECK (schema_version > 0),
    snapshot       boolean     NOT NULL,
    payload        jsonb       NOT NULL,
    UNIQUE (agent_id, sequence)
);

CREATE INDEX platform_lab_capacity_observations_time_idx
    ON platform_lab_capacity_observations (observed_at DESC, id DESC);
