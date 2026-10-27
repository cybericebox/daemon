-- Raw lab telemetry is archived before it is deleted: the lab agent sends a
-- snapshot or delta per team about every 5 s, the rollups keep only
-- aggregates, and the archive keeps the rows themselves so every rollup can
-- be recomputed (and new ones added) later.
--
-- One chunk is the raw rows of one event received in one hour (a busy hour is
-- split by size), as zstd-compressed NDJSON: one JSON object per line with
-- every column of event_lab_observations, ordered by team, lab group and
-- observation time so that neighbouring lines look alike and compress well
-- (about 150 times). first_/last_ is the arrival order range, the same order
-- the rollups use: the chunks of an event tile it without gaps or overlaps.
CREATE TABLE event_lab_observation_archives
(
    id                uuid PRIMARY KEY,
    event_id          uuid        NOT NULL REFERENCES events (id) ON DELETE CASCADE,
    period_start      timestamptz NOT NULL,
    period_end        timestamptz NOT NULL CHECK (period_end > period_start),
    row_count         integer     NOT NULL CHECK (row_count > 0),
    first_received_at timestamptz NOT NULL,
    first_id          uuid        NOT NULL,
    last_received_at  timestamptz NOT NULL,
    last_id           uuid        NOT NULL,
    codec             text        NOT NULL,
    data              bytea       NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, last_received_at, last_id)
);

-- The data is compressed already.
ALTER TABLE event_lab_observation_archives
    ALTER COLUMN data SET STORAGE EXTERNAL;

CREATE INDEX event_lab_observation_archives_event_period_idx
    ON event_lab_observation_archives (event_id, period_start, first_received_at, first_id);

-- The archive cursor is the last observation archived (arrival order), like
-- the VPN and lab usage cursors. The purge of raw rows waits for it.
ALTER TABLE event_analytics_rollups
    ADD COLUMN lab_archive_cursor_received_at timestamptz,
    ADD COLUMN lab_archive_cursor_id          uuid;
