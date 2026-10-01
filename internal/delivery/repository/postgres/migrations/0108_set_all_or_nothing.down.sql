-- Restored tasks and set-wide visibility stay (both are valid per-task data).
ALTER TABLE event_exercises
    ADD COLUMN excluded_task_ids uuid[] NOT NULL DEFAULT '{}';
