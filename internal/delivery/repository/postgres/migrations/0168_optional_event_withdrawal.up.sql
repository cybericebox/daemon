-- A scheduled finish ends runtime without requiring removal of public visibility.
-- Existing dates remain untouched.
ALTER TABLE events
    DROP CONSTRAINT events_lifecycle_times_check,
    ADD CONSTRAINT events_lifecycle_times_check CHECK (
        start_at >= publish_at
        AND (finish_at IS NULL OR finish_at > start_at)
        AND (withdraw_at IS NULL OR (finish_at IS NOT NULL AND withdraw_at > finish_at))
        AND (manual_finished_at IS NULL
            OR (manual_finished_at >= start_at
                AND (withdraw_at IS NULL OR manual_finished_at < withdraw_at)))
    );
