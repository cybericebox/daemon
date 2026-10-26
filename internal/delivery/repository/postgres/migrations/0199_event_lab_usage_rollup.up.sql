-- Lab resource usage rolled up from the raw lab telemetry. The lab agent
-- sends a full snapshot per team about every 5 s (~2.6 KB), which made
-- event_lab_observations most of the database. The stand analytics reads
-- this table instead, so the raw rows can go once they are rolled up.
--
-- One row is one device (container) of one lab of one team's lab group in
-- one minute. cpu_* count only samples with a usage reading
-- (usageAvailable); the average is cpu_sum / cpu_samples. Kept as long as
-- the event: it goes with the event (also in the archive).
CREATE TABLE event_lab_usage_minutes
(
    event_id       uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    team_id        uuid        NOT NULL,
    lab_group_name text        NOT NULL,
    lab_name       text        NOT NULL,
    device_name    text        NOT NULL,
    bucket_at      timestamptz NOT NULL,
    samples        integer     NOT NULL CHECK (samples > 0),
    cpu_samples    integer     NOT NULL CHECK (cpu_samples >= 0),
    cpu_sum        bigint      NOT NULL CHECK (cpu_sum >= 0),
    cpu_max        bigint,
    memory_max     bigint,
    restart_max    bigint      NOT NULL CHECK (restart_max >= 0),
    PRIMARY KEY (event_id, team_id, lab_group_name, lab_name, device_name, bucket_at)
);

CREATE INDEX event_lab_usage_minutes_event_time_idx ON event_lab_usage_minutes (event_id, bucket_at);

-- The lab usage rollup reads each event's observations in arrival order,
-- like the VPN one; the cursor is the last observation folded in.
ALTER TABLE event_analytics_rollups
    ADD COLUMN lab_usage_cursor_received_at timestamptz,
    ADD COLUMN lab_usage_cursor_id          uuid,
    ADD COLUMN lab_usage_finalized_at       timestamptz;
