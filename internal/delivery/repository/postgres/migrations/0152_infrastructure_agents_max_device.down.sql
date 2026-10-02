ALTER TABLE infrastructure_agents
    DROP COLUMN IF EXISTS max_device_memory_bytes,
    DROP COLUMN IF EXISTS max_device_cpu_millicores;
