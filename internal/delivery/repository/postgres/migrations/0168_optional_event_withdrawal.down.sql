-- Restore the previous invariant. Downgrade is refused while finish-only schedules
-- exist: owners must explicitly update those schedules first. Never invent a withdrawal.
ALTER TABLE events
    DROP CONSTRAINT events_lifecycle_times_check,
    ADD CONSTRAINT events_lifecycle_times_check CHECK (
        start_at >= publish_at
        AND (
            (finish_at IS NULL AND withdraw_at IS NULL)
            OR (finish_at IS NOT NULL AND withdraw_at IS NOT NULL
                AND finish_at > start_at AND withdraw_at > finish_at)
        )
        AND (manual_finished_at IS NULL
            OR (manual_finished_at >= start_at
                AND (withdraw_at IS NULL OR manual_finished_at < withdraw_at)))
    );
