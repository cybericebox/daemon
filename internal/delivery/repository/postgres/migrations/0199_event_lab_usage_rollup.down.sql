ALTER TABLE event_analytics_rollups
    DROP COLUMN lab_usage_finalized_at,
    DROP COLUMN lab_usage_cursor_id,
    DROP COLUMN lab_usage_cursor_received_at;

DROP TABLE event_lab_usage_minutes;
