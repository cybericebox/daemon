ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_lifecycle_times_check,
    ADD COLUMN IF NOT EXISTS event_type smallint NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS scored boolean NOT NULL DEFAULT true,
    ADD COLUMN IF NOT EXISTS schedule smallint NOT NULL DEFAULT 0;

UPDATE events
SET schedule = CASE WHEN finish_at IS NULL THEN 1 ELSE 0 END;

ALTER TABLE events
    ADD CONSTRAINT events_event_type_check CHECK (event_type IN (0, 1)),
    ADD CONSTRAINT events_schedule_check CHECK (schedule IN (0, 1)),
    ADD CONSTRAINT events_lifecycle_times_check CHECK (
        start_at >= publish_at
        AND (
            (schedule = 0 AND finish_at IS NOT NULL AND withdraw_at IS NOT NULL
                AND finish_at > start_at AND withdraw_at > finish_at)
            OR (schedule = 1 AND finish_at IS NULL AND withdraw_at IS NULL)
        )
        AND (manual_finished_at IS NULL
            OR (manual_finished_at >= start_at
                AND (withdraw_at IS NULL OR manual_finished_at < withdraw_at)))
    );

ALTER TABLE event_configs
    ADD COLUMN IF NOT EXISTS registration_after_start boolean NOT NULL DEFAULT false;
