-- The live screen is staff-only again (projector PCs use screen links), so
-- the live audience setting of 0109 goes away.
ALTER TABLE event_configs
    DROP COLUMN results_live_audience;
