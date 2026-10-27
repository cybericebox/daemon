-- A recompute of an event's lab telemetry (rebuilding the minute tables and
-- the transitions journal from the archive) restores archived raw rows and
-- resets the rollup cursors. While the hold is in the future the analytics
-- job does not roll up the event's lab telemetry and the retention job does
-- not delete its raw rows, so neither races the recompute. The hold expires
-- by itself if the recompute dies.
ALTER TABLE event_analytics_rollups
    ADD COLUMN lab_telemetry_hold_until timestamptz;
