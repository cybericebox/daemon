ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_lifecycle_times_check,
    DROP CONSTRAINT IF EXISTS events_event_type_check,
    DROP CONSTRAINT IF EXISTS events_schedule_check,
    DROP COLUMN IF EXISTS event_type,
    DROP COLUMN IF EXISTS scored,
    DROP COLUMN IF EXISTS schedule;

ALTER TABLE events
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

ALTER TABLE event_configs
    DROP COLUMN IF EXISTS registration_after_start;
