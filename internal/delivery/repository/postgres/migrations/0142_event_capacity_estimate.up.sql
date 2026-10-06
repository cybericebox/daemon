-- The organizer's estimate of the laboratory load of tasks that are not final yet: how many
-- dynamic tasks are still expected and how big an average one is (requests = limits).
ALTER TABLE event_configs
    ADD COLUMN capacity_expected_dynamic_tasks  integer NOT NULL DEFAULT 0 CHECK (capacity_expected_dynamic_tasks BETWEEN 0 AND 1000),
    ADD COLUMN capacity_avg_task_cpu_millicores integer NOT NULL DEFAULT 0 CHECK (capacity_avg_task_cpu_millicores >= 0),
    ADD COLUMN capacity_avg_task_memory_bytes   bigint  NOT NULL DEFAULT 0 CHECK (capacity_avg_task_memory_bytes >= 0);
