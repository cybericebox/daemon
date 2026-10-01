-- The merged CURRENT state of every team lab group, maintained by the
-- monitoring runner (snapshot replaces, deltas merge). event_lab_observations
-- stays the immutable history; reading "now" from it would return a lone delta.
CREATE TABLE lab_monitoring_current
(
    event_id       uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    event_team_id  uuid        NOT NULL REFERENCES event_teams (id) ON DELETE CASCADE,
    lab_group_name text        NOT NULL,
    agent_id       text        NOT NULL,
    sequence       bigint      NOT NULL CHECK (sequence > 0),
    observed_at    timestamptz NOT NULL,
    updated_at     timestamptz NOT NULL,
    payload        jsonb       NOT NULL,
    PRIMARY KEY (event_id, event_team_id, lab_group_name)
);

CREATE INDEX lab_monitoring_current_agent_idx ON lab_monitoring_current (agent_id, updated_at);
CREATE INDEX lab_monitoring_current_updated_idx ON lab_monitoring_current (updated_at DESC);
