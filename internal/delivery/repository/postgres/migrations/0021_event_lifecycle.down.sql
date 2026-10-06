ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_lifecycle_times_check,
    DROP CONSTRAINT IF EXISTS events_schedule_check,
    DROP CONSTRAINT IF EXISTS events_join_policy_check,
    DROP CONSTRAINT IF EXISTS events_event_type_check,
    DROP COLUMN IF EXISTS manual_finished_at,
    DROP COLUMN IF EXISTS withdraw_at,
    DROP COLUMN IF EXISTS finish_at,
    DROP COLUMN IF EXISTS start_at,
    DROP COLUMN IF EXISTS publish_at,
    DROP COLUMN IF EXISTS schedule,
    DROP COLUMN IF EXISTS join_policy,
    DROP COLUMN IF EXISTS scored,
    DROP COLUMN IF EXISTS event_type;
