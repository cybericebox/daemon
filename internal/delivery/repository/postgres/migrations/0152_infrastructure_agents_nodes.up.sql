-- The allocatable room of each lab node of an agent, as the agent last reported it (a JSON array of
-- {name, cpu_millicores, memory_bytes}), so the resource calendar can pack devices: a device must fit one
-- node. NULL means the agent does not report it (an older agent): its whole capacity counts as one node.
ALTER TABLE infrastructure_agents
    ADD COLUMN capacity_nodes jsonb;
