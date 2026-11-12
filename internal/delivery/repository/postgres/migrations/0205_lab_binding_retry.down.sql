ALTER TABLE lab_bindings
    DROP COLUMN wait_since,
    DROP COLUMN wait_reason,
    DROP COLUMN retry_attempts,
    DROP COLUMN failed_at;
