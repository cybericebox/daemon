DROP INDEX IF EXISTS infrastructure_agents_admin_endpoint_idx;
CREATE UNIQUE INDEX infrastructure_agents_admin_endpoint_idx
    ON infrastructure_agents (endpoint) WHERE source = 'admin';
ALTER TABLE infrastructure_agents
    DROP COLUMN IF EXISTS archived_at,
    DROP COLUMN IF EXISTS capacity_seen_at,
    DROP COLUMN IF EXISTS capacity_memory_bytes,
    DROP COLUMN IF EXISTS capacity_cpu_millicores;
