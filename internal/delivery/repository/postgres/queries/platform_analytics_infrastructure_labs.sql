-- Platform analytics, «Інфраструктура»: the active time split by lab kind. The
-- kinds are the event teams' stands, the moderators team stand and the catalog
-- test labs (exercise test deploys). A test lab is active from the creation of
-- its deploy to its removal (exercise_test_deploy_runs, written by triggers);
-- a stand is active while creating (1) or ready (2) (event_stand_transitions).
-- Every window is clipped to [from_at, least(to_at, now_at)).

-- name: ListPlatformInfraHoursByKind :many
-- One row per kind ('event', 'moderators', 'test'), also when a kind has no
-- time: hours is the summed active time in the period, labs the number of
-- distinct stands / test labs that were active.
WITH win AS (
    SELECT sqlc.arg(from_at)::timestamptz AS from_at,
           LEAST(sqlc.arg(to_at)::timestamptz, sqlc.arg(now_at)::timestamptz) AS to_at
), seg AS (
    SELECT x.event_id, x.team_id, x.to_status, x.at AS start_at,
           COALESCE(lead(x.at) OVER (PARTITION BY x.event_id, x.team_id ORDER BY x.at, x.id), w.to_at) AS end_at
    FROM event_stand_transitions x
    CROSS JOIN win w
    WHERE x.source = 'stand'
      AND x.at < w.to_at
), stand_active AS (
    SELECT CASE WHEN t.moderators THEN 'moderators' ELSE 'event' END AS kind,
           s.team_id::text AS lab,
           GREATEST(s.start_at, w.from_at) AS s,
           LEAST(s.end_at, w.to_at)        AS e
    FROM seg s
    CROSS JOIN win w
    LEFT JOIN event_teams t ON t.id = s.team_id
    WHERE s.to_status IN (1, 2)
      AND s.end_at > w.from_at
      AND s.start_at < w.to_at
), test_active AS (
    SELECT 'test' AS kind,
           r.deploy_id::text AS lab,
           GREATEST(r.started_at, w.from_at)                   AS s,
           LEAST(COALESCE(r.ended_at, w.to_at), w.to_at)       AS e
    FROM exercise_test_deploy_runs r
    CROSS JOIN win w
    WHERE r.started_at < w.to_at
      AND COALESCE(r.ended_at, w.to_at) > w.from_at
), active AS (
    SELECT * FROM stand_active
    UNION ALL
    SELECT * FROM test_active
)
SELECT k.kind::text AS kind,
       (COALESCE(sum(extract(epoch FROM a.e - a.s)), 0) / 3600.0)::float8 AS hours,
       count(DISTINCT a.lab)::bigint AS labs
FROM (VALUES ('event'), ('moderators'), ('test')) AS k(kind)
LEFT JOIN active a ON a.kind = k.kind
GROUP BY k.kind
ORDER BY CASE k.kind WHEN 'event' THEN 1 WHEN 'moderators' THEN 2 ELSE 3 END;

-- name: ListPlatformInfraPeaksByKind :many
-- Peak of concurrently active labs per bucket ("hour" or "day", UTC), like
-- ListPlatformInfraPeaks, over the chosen kinds: with_stands = team stands
-- (event and moderators), with_tests = catalog test labs. Both = every lab.
WITH win AS (
    SELECT sqlc.arg(from_at)::timestamptz AS from_at,
           LEAST(sqlc.arg(to_at)::timestamptz, sqlc.arg(now_at)::timestamptz) AS to_at
), seg AS (
    SELECT x.to_status, x.at AS start_at,
           COALESCE(lead(x.at) OVER (PARTITION BY x.event_id, x.team_id ORDER BY x.at, x.id), w.to_at) AS end_at
    FROM event_stand_transitions x
    CROSS JOIN win w
    WHERE sqlc.arg(with_stands)::boolean
      AND x.source = 'stand'
      AND x.at < w.to_at
), active AS (
    SELECT GREATEST(s.start_at, w.from_at) AS s,
           LEAST(s.end_at, w.to_at)        AS e
    FROM seg s
    CROSS JOIN win w
    WHERE s.to_status IN (1, 2)
      AND s.end_at > w.from_at
      AND s.start_at < w.to_at
    UNION ALL
    SELECT GREATEST(r.started_at, w.from_at),
           LEAST(COALESCE(r.ended_at, w.to_at), w.to_at)
    FROM exercise_test_deploy_runs r
    CROSS JOIN win w
    WHERE sqlc.arg(with_tests)::boolean
      AND r.started_at < w.to_at
      AND COALESCE(r.ended_at, w.to_at) > w.from_at
), buckets AS (
    SELECT g AS b
    FROM win w,
         generate_series(date_trunc(sqlc.arg(bucket)::text, w.from_at AT TIME ZONE 'UTC'),
                         date_trunc(sqlc.arg(bucket)::text, (w.to_at - interval '1 microsecond') AT TIME ZONE 'UTC'),
                         ('1 ' || sqlc.arg(bucket)::text)::interval) g
    WHERE w.from_at < w.to_at
), points AS (
    SELECT e AS ts, 0 AS ord, -1 AS delta FROM active
    UNION ALL
    SELECT b AT TIME ZONE 'UTC', 1, 0 FROM buckets
    UNION ALL
    SELECT s, 2, 1 FROM active
), running AS (
    SELECT ts, sum(delta) OVER (ORDER BY ts, ord ROWS UNBOUNDED PRECEDING) AS level
    FROM points
)
SELECT (date_trunc(sqlc.arg(bucket)::text, ts AT TIME ZONE 'UTC') AT TIME ZONE 'UTC')::timestamptz AS bucket_at,
       GREATEST(max(level), 0)::bigint AS peak
FROM running
WHERE ts < (SELECT to_at FROM win)
GROUP BY 1
ORDER BY 1;
