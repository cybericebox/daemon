DROP INDEX IF EXISTS infrastructure_agents_endpoint_idx;
CREATE UNIQUE INDEX infrastructure_agents_admin_endpoint_idx
    ON infrastructure_agents (endpoint) WHERE source = 'admin' AND archived_at IS NULL;
