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

-- Which raw rows a chunk really holds. The cursor says how far the archive got by position, but a row committed
-- late can sit behind it; a raw row is deleted only when it is listed here, and the retention job archives the
-- late ones in a chunk of their own.
CREATE TABLE event_lab_archived_rows
(
    row_id   uuid PRIMARY KEY,
    chunk_id uuid NOT NULL REFERENCES event_lab_observation_archives (id) ON DELETE CASCADE
);

CREATE INDEX event_lab_archived_rows_chunk_idx ON event_lab_archived_rows (chunk_id);

-- The archive cursor is the last observation archived (arrival order), like
-- the VPN and lab usage cursors. The purge of raw rows waits for it.
ALTER TABLE event_analytics_rollups
    ADD COLUMN lab_archive_cursor_received_at timestamptz,
    ADD COLUMN lab_archive_cursor_id          uuid;
