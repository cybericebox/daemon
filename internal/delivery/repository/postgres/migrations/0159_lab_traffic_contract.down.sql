-- Restore the legacy sentinel representation before restoring NOT NULL.
UPDATE event_lab_touches
SET first_seen_at = COALESCE(first_seen_at, '0001-01-01 00:00:00+00'::timestamptz),
    last_seen_at = COALESCE(last_seen_at, '0001-01-01 00:00:00+00'::timestamptz)
WHERE first_seen_at IS NULL OR last_seen_at IS NULL;

ALTER TABLE event_lab_touches
    ALTER COLUMN first_seen_at SET NOT NULL,
    ALTER COLUMN last_seen_at SET NOT NULL,
    DROP COLUMN lab_initiated_attempts_count;

ALTER TABLE lab_traffic_coverage DROP COLUMN explicit;
