-- Participant action times are absent for traffic initiated only by the lab.
ALTER TABLE event_lab_touches
    ALTER COLUMN first_seen_at DROP NOT NULL,
    ALTER COLUMN last_seen_at DROP NOT NULL,
    ADD COLUMN lab_initiated_attempts_count bigint NOT NULL DEFAULT 0;

-- Older zero-valued Go dates were persisted as year one. They mean missing.
UPDATE event_lab_touches
SET first_seen_at = NULLIF(first_seen_at, '0001-01-01 00:00:00+00'::timestamptz),
    last_seen_at = NULLIF(last_seen_at, '0001-01-01 00:00:00+00'::timestamptz),
    first_responded_at = NULLIF(first_responded_at, '0001-01-01 00:00:00+00'::timestamptz)
WHERE first_seen_at = '0001-01-01 00:00:00+00'::timestamptz
   OR last_seen_at = '0001-01-01 00:00:00+00'::timestamptz
   OR first_responded_at = '0001-01-01 00:00:00+00'::timestamptz;

ALTER TABLE lab_traffic_coverage ADD COLUMN explicit boolean NOT NULL DEFAULT false;
