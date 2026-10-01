-- The values stay; only the requirement goes (the per-task own static
-- scoring set by the up migration is kept: it scores the same).
ALTER TABLE events
    DROP CONSTRAINT IF EXISTS events_static_points_required_check,
    ALTER COLUMN static_points DROP DEFAULT;
