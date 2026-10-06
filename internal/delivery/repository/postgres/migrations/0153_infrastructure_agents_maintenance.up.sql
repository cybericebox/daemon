-- The maintenance windows the cluster operator announced on an agent (a JSON array of {name, reason, from, to,
-- all_tenants, has_capacity, cpu_millicores, memory_bytes}), as the agent last reported them. The resource calendar
-- gives the agent no capacity inside a window (or what the window leaves). NULL means the agent has not reported its
-- windows (an older agent, or the CRD is not installed); an empty array is a report of none.
ALTER TABLE infrastructure_agents
    ADD COLUMN maintenance_windows jsonb;
