-- The last node profile report of each agent (container, microvm: availability and the pod overhead), kept so a
-- replica that does not monitor, and a restart, know it before the next report. Every report overwrites the row.
CREATE TABLE infrastructure_agent_node_profiles
(
    agent_id    uuid PRIMARY KEY REFERENCES infrastructure_agents (id) ON DELETE CASCADE,
    profiles    jsonb       NOT NULL,
    received_at timestamptz NOT NULL
);
COMMENT ON TABLE infrastructure_agent_node_profiles IS 'Last node profile report of an agent (CapacityResponse.node_profiles); one row per agent';
