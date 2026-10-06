-- The largest device an agent can place, as it last reported it (per resource, the largest allocatable of a lab node net
-- of the platform reserve; the agent never shows the cluster layout, only this one amount). The resource calendar refuses to
-- plan a device above it. NULL means the agent does not report it (an older agent, or no schedulable node).
ALTER TABLE infrastructure_agents
    ADD COLUMN max_device_cpu_millicores bigint CHECK (max_device_cpu_millicores >= 0),
    ADD COLUMN max_device_memory_bytes   bigint CHECK (max_device_memory_bytes >= 0);
