ALTER TABLE event_analytics_rollups
    DROP COLUMN lab_journal_cursor_id,
    DROP COLUMN lab_journal_cursor_received_at;

DROP TABLE event_lab_journal_states;
DROP TABLE event_lab_transitions;
DROP TABLE event_vpn_client_minutes;
DROP TABLE event_lab_minutes;

ALTER TABLE event_lab_usage_minutes
    DROP COLUMN last_phase_at,
    DROP COLUMN last_pod_phase,
    DROP COLUMN ready_samples,
    DROP COLUMN memory_samples,
    DROP COLUMN memory_sum;
