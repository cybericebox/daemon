ALTER TABLE infrastructure_agents
    ADD COLUMN features jsonb,
    ADD COLUMN features_at timestamptz;
COMMENT ON COLUMN infrastructure_agents.features IS 'Last laboratory features read from the agent (persistence, image cache, scheduler, endpoints, certificate); NULL until first read';
COMMENT ON COLUMN infrastructure_agents.features_at IS 'When features was last read successfully';
