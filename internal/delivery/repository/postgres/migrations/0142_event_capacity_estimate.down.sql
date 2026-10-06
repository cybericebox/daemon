ALTER TABLE event_configs
    DROP COLUMN IF EXISTS capacity_avg_task_memory_bytes,
    DROP COLUMN IF EXISTS capacity_avg_task_cpu_millicores,
    DROP COLUMN IF EXISTS capacity_expected_dynamic_tasks;
