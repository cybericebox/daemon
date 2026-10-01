-- The last known capacity of an agent, kept so that an offline or maintained agent does not break the
-- planning. The values are the tenant quota; a NULL value with capacity_seen_at set means the tenant has
-- no limit on that resource, and capacity_seen_at NULL means the agent was never seen (no capacity).
-- archived_at marks a deleted agent: its record stays for history, its keys and endpoint are wiped.
ALTER TABLE infrastructure_agents
    ADD COLUMN capacity_cpu_millicores bigint CHECK (capacity_cpu_millicores >= 0),
    ADD COLUMN capacity_memory_bytes   bigint CHECK (capacity_memory_bytes >= 0),
    ADD COLUMN capacity_seen_at        timestamptz,
    ADD COLUMN archived_at             timestamptz;

DROP INDEX infrastructure_agents_admin_endpoint_idx;
CREATE UNIQUE INDEX infrastructure_agents_admin_endpoint_idx
    ON infrastructure_agents (endpoint) WHERE source = 'admin' AND archived_at IS NULL;
