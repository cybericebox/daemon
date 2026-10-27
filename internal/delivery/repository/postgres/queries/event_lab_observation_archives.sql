-- Archive of the raw lab observations (docs/LAB-TRAFFIC-ACCOUNTING.md): the
-- rows are written as zstd NDJSON chunks, one event and hour at a time,
-- before the retention job may delete them.

-- name: NextEventLabObservationArchiveRange :many
-- The next chunk of an event's observations after the archive cursor: up to
-- max_rows rows in arrival order, all received in the hour of the first one
-- and before closed_before (only closed hours are archived, so a chunk never
-- grows afterwards). No row when nothing is left to archive.
WITH first_row AS (
    SELECT o.received_at
    FROM event_lab_observations o
    WHERE o.event_id = sqlc.arg(event_id)
      AND o.received_at < sqlc.arg(closed_before)::timestamptz
      AND (o.received_at, o.id) > (sqlc.arg(after_received_at)::timestamptz, sqlc.arg(after_id)::uuid)
    ORDER BY o.received_at, o.id
    LIMIT 1
), picked AS (
    SELECT o.id, o.received_at
    FROM event_lab_observations o
    CROSS JOIN first_row f
    WHERE o.event_id = sqlc.arg(event_id)
      AND (o.received_at, o.id) > (sqlc.arg(after_received_at)::timestamptz, sqlc.arg(after_id)::uuid)
      AND o.received_at < LEAST(sqlc.arg(closed_before)::timestamptz,
                                date_bin('1 hour', f.received_at, 'epoch'::timestamptz) + interval '1 hour')
    ORDER BY o.received_at, o.id
    LIMIT sqlc.arg(max_rows)
)
SELECT (SELECT count(*) FROM picked)::integer                                                AS row_count,
       date_bin('1 hour', first_pick.received_at, 'epoch'::timestamptz)::timestamptz         AS period_start,
       first_pick.received_at::timestamptz                                                   AS first_received_at,
       first_pick.id::uuid                                                                   AS first_id,
       last_pick.received_at::timestamptz                                                    AS last_received_at,
       last_pick.id::uuid                                                                    AS last_id
FROM (SELECT p.received_at, p.id FROM picked p ORDER BY p.received_at, p.id LIMIT 1) first_pick
CROSS JOIN (SELECT p.received_at, p.id FROM picked p ORDER BY p.received_at DESC, p.id DESC LIMIT 1) last_pick;

-- name: ListEventLabObservationArchiveLines :many
-- The rows of a chunk as JSON lines with every column, ordered by team, lab
-- group and observation time so that neighbours resemble each other.
SELECT to_jsonb(o)::text AS line
FROM event_lab_observations o
WHERE o.event_id = sqlc.arg(event_id)
  AND (o.received_at, o.id) > (sqlc.arg(after_received_at)::timestamptz, sqlc.arg(after_id)::uuid)
  AND (o.received_at, o.id) <= (sqlc.arg(last_received_at)::timestamptz, sqlc.arg(last_id)::uuid)
ORDER BY o.event_team_id, o.lab_group_name, o.observed_at, o.id;

-- name: SaveEventLabObservationArchive :exec
-- Stores a chunk and moves the archive cursor past it in one statement: a
-- failure leaves both untouched, a re-run of the same chunk adds nothing.
WITH chunk AS (
    INSERT INTO event_lab_observation_archives (id, event_id, period_start, period_end, row_count,
                                                first_received_at, first_id, last_received_at, last_id, codec, data)
    VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(period_start), sqlc.arg(period_end), sqlc.arg(row_count),
            sqlc.arg(first_received_at), sqlc.arg(first_id), sqlc.arg(last_received_at), sqlc.arg(last_id),
            sqlc.arg(codec), sqlc.arg(data))
    ON CONFLICT (event_id, last_received_at, last_id) DO NOTHING
    RETURNING 1
)
INSERT INTO event_analytics_rollups (event_id, lab_archive_cursor_received_at, lab_archive_cursor_id)
VALUES (sqlc.arg(event_id), sqlc.arg(last_received_at), sqlc.arg(last_id))
ON CONFLICT (event_id) DO UPDATE
    SET lab_archive_cursor_received_at = EXCLUDED.lab_archive_cursor_received_at,
        lab_archive_cursor_id          = EXCLUDED.lab_archive_cursor_id;

-- name: ListEventLabObservationArchiveChunks :many
-- The archived chunks of an event in arrival order, from one point on, with
-- their data. limit_rows bounds a page; a reader continues after the last
-- chunk it got.
SELECT id, event_id, period_start, period_end, row_count, first_received_at, first_id, last_received_at, last_id,
       codec, data
FROM event_lab_observation_archives
WHERE event_id = sqlc.arg(event_id)
  AND (last_received_at, last_id) > (sqlc.arg(after_received_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY last_received_at, last_id
LIMIT sqlc.arg(limit_rows);

-- name: ListEventLabObservationsAfter :many
-- Raw observations still in the table after a point in arrival order (the
-- ones the archive does not hold yet, or all while they are kept), up to
-- limit_rows, as JSON lines in the archive format.
SELECT o.received_at, o.id, to_jsonb(o)::text AS line
FROM event_lab_observations o
WHERE o.event_id = sqlc.arg(event_id)
  AND (o.received_at, o.id) > (sqlc.arg(after_received_at)::timestamptz, sqlc.arg(after_id)::uuid)
ORDER BY o.received_at, o.id
LIMIT sqlc.arg(limit_rows);
