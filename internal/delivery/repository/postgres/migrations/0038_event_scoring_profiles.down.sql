DROP TABLE IF EXISTS event_scoring_populations;

ALTER TABLE event_challenges
    DROP CONSTRAINT IF EXISTS event_challenges_scoring_profile_check,
    DROP COLUMN IF EXISTS dynamic_floor_at_percent,
    DROP COLUMN IF EXISTS dynamic_max_points,
    DROP COLUMN IF EXISTS dynamic_min_points,
    DROP COLUMN IF EXISTS dynamic_algorithm,
    DROP COLUMN IF EXISTS scoring_mode;

ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_dynamic_bounds_check,
    DROP CONSTRAINT IF EXISTS events_dynamic_algorithm_check,
    DROP CONSTRAINT IF EXISTS events_scoring_mode_check,
    DROP COLUMN IF EXISTS force_event_scoring,
    DROP COLUMN IF EXISTS dynamic_floor_at_percent,
    DROP COLUMN IF EXISTS dynamic_max_points,
    DROP COLUMN IF EXISTS dynamic_min_points,
    DROP COLUMN IF EXISTS dynamic_algorithm,
    DROP COLUMN IF EXISTS scoring_mode;
