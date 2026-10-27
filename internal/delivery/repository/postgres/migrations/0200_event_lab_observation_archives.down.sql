ALTER TABLE event_analytics_rollups
    DROP COLUMN lab_archive_cursor_received_at,
    DROP COLUMN lab_archive_cursor_id;

DROP TABLE event_lab_observation_archives;
