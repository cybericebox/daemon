-- Platform analytics, «Інфраструктура» (docs/EVENT-ANALYTICS.md §8). Aggregates
-- only: no IP addresses, no VPN client addresses, no lab payloads.
--
-- A stand is ACTIVE while its status is creating (1) or ready (2), the same
-- definition as the infrastructure summary of the admin labs page. The moderators
-- stand counts too: it takes cluster resources like any other. Every window is
-- clipped to [from_at, least(to_at, now_at)) so an open stand does not run into
-- the future.

-- name: ListPlatformInfraStandHours :many
-- Stand-hours per event: the summed active time of the event's stands inside the
-- period, in hours, top events first. total_hours / total_events cover every
-- event, not only the returned top rows. The status log of a stand is walked
-- with lead(): a transition lasts until the next one, the last one until the
-- end of the window.
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
), active AS (
    SELECT s.event_id, s.team_id,
           GREATEST(s.start_at, w.from_at) AS s,
           LEAST(s.end_at, w.to_at)        AS e
    FROM seg s
    CROSS JOIN win w
    WHERE s.to_status IN (1, 2)
      AND s.end_at > w.from_at
      AND s.start_at < w.to_at
), per_event AS (
    SELECT a.event_id,
           sum(extract(epoch FROM a.e - a.s))::float8 / 3600.0 AS hours,
           count(DISTINCT a.team_id)::bigint                   AS stands
    FROM active a
    GROUP BY a.event_id
)
SELECT p.event_id,
       COALESCE(ev.name, '')::text                          AS event_name,
       p.hours::float8                                      AS hours,
       p.stands                                             AS stands,
       (sum(p.hours) OVER ())::float8                       AS total_hours,
       (count(*) OVER ())::bigint                           AS total_events
FROM per_event p
LEFT JOIN events ev ON ev.id = p.event_id
ORDER BY p.hours DESC, p.event_id
LIMIT sqlc.arg(limit_val);

-- name: ListPlatformInfraPeaks :many
-- Peak of concurrently active stands per bucket ("hour" or "day", UTC). The
-- active intervals become +1 / -1 points; a running sum gives the level, and a
-- zero point at every bucket start carries the level over from the previous
-- bucket. At one instant the order is: ends, bucket start, starts.
WITH win AS (
    SELECT sqlc.arg(from_at)::timestamptz AS from_at,
           LEAST(sqlc.arg(to_at)::timestamptz, sqlc.arg(now_at)::timestamptz) AS to_at
), seg AS (
    SELECT x.to_status, x.at AS start_at,
           COALESCE(lead(x.at) OVER (PARTITION BY x.event_id, x.team_id ORDER BY x.at, x.id), w.to_at) AS end_at
    FROM event_stand_transitions x
    CROSS JOIN win w
    WHERE x.source = 'stand'
      AND x.at < w.to_at
), active AS (
    SELECT GREATEST(s.start_at, w.from_at) AS s,
           LEAST(s.end_at, w.to_at)        AS e
    FROM seg s
    CROSS JOIN win w
    WHERE s.to_status IN (1, 2)
      AND s.end_at > w.from_at
      AND s.start_at < w.to_at
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

-- name: ListPlatformInfraFailures :many
-- Failed laboratories and stands in the period grouped by a reason code. The
-- code is derived from the stored failure text (eventStand.Classify and the
-- deploy engine write a fixed set of prefixes); the raw text never leaves the
-- database, so nothing team- or user-specific is returned. labs counts lab
-- status transitions to failed (source 'lab'), stands counts stand transitions
-- to failed (source 'stand').
WITH f AS (
    SELECT x.source, x.event_id, x.team_id, x.at,
           CASE
               WHEN x.reason LIKE 'ErrImagePull%' OR x.reason LIKE 'ImagePullBackOff%' OR x.reason LIKE 'InvalidImageName%' THEN 'image_pull'
               WHEN x.reason LIKE 'CrashLoopBackOff%' THEN 'crash_loop'
               WHEN x.reason LIKE 'CreateContainerConfigError%' THEN 'container_config'
               WHEN x.reason LIKE 'Laboratory reported phase%' THEN 'lab_phase_failed'
               WHEN x.reason LIKE 'Not ready after%' THEN 'deploy_timeout'
               WHEN x.reason LIKE 'Laboratory status unavailable%' THEN 'status_unavailable'
               WHEN x.reason LIKE 'Topology unavailable%' THEN 'topology_unavailable'
               WHEN x.reason LIKE 'Deploy failed%' THEN 'deploy_failed'
               WHEN COALESCE(x.reason, '') = '' THEN 'unknown'
               ELSE 'other'
           END AS code
    FROM event_stand_transitions x
    WHERE x.at >= sqlc.arg(from_at)::timestamptz
      AND x.at < sqlc.arg(to_at)::timestamptz
      AND ((x.source = 'lab' AND x.to_status = 2) OR (x.source = 'stand' AND x.to_status = 3))
)
SELECT f.code::text                                          AS code,
       count(*) FILTER (WHERE f.source = 'lab')::bigint      AS labs,
       count(*) FILTER (WHERE f.source = 'stand')::bigint    AS stands,
       count(DISTINCT f.event_id)::bigint                    AS events,
       max(f.at)::timestamptz                                AS last_at
FROM f
GROUP BY f.code
ORDER BY count(*) FILTER (WHERE f.source = 'lab') DESC, count(*) FILTER (WHERE f.source = 'stand') DESC, f.code;

-- name: ListPlatformInfraCapacity :many
-- Capacity over time from the agents' capacity observations, downsampled to buckets of
-- step_seconds (the caller picks the step so a period has at most about 300 points). Inside a bucket
-- every agent contributes its average; the agents are then summed. An agent reports the platform's
-- own tenant view: the quota is the capacity and what its pods request is the reservation
-- (cpuQuotaMillicores / cpuReservedMillicores, memoryQuotaBytes / memoryReservedBytes); samples
-- recorded before tenancy carry allocatable* / requested* and are read as before. The payload
-- carries int64 values as JSON numbers or numeric strings, both are read.
WITH raw AS (
    SELECT to_timestamp(floor(extract(epoch FROM o.observed_at) / sqlc.arg(step_seconds)::float8) * sqlc.arg(step_seconds)::float8) AS bucket,
           o.agent_id,
           COALESCE(o.payload ->> 'cpuQuotaMillicores', o.payload ->> 'allocatableCpuMillicores')   AS alloc_cpu,
           COALESCE(o.payload ->> 'cpuReservedMillicores', o.payload ->> 'requestedCpuMillicores')  AS req_cpu,
           COALESCE(o.payload ->> 'memoryQuotaBytes', o.payload ->> 'allocatableMemoryBytes')       AS alloc_mem,
           COALESCE(o.payload ->> 'memoryReservedBytes', o.payload ->> 'requestedMemoryBytes')      AS req_mem
    FROM platform_lab_capacity_observations o
    WHERE o.observed_at >= sqlc.arg(from_at)::timestamptz
      AND o.observed_at < sqlc.arg(to_at)::timestamptz
), obs AS (
    SELECT bucket, agent_id,
           CASE WHEN alloc_cpu ~ '^[0-9]+(\.[0-9]+)?$' THEN alloc_cpu::numeric END AS alloc_cpu,
           CASE WHEN req_cpu ~ '^[0-9]+(\.[0-9]+)?$' THEN req_cpu::numeric END     AS req_cpu,
           CASE WHEN alloc_mem ~ '^[0-9]+(\.[0-9]+)?$' THEN alloc_mem::numeric END AS alloc_mem,
           CASE WHEN req_mem ~ '^[0-9]+(\.[0-9]+)?$' THEN req_mem::numeric END     AS req_mem
    FROM raw
), per_agent AS (
    SELECT bucket, agent_id,
           avg(alloc_cpu) AS alloc_cpu, avg(req_cpu) AS req_cpu,
           avg(alloc_mem) AS alloc_mem, avg(req_mem) AS req_mem
    FROM obs
    WHERE alloc_cpu IS NOT NULL OR alloc_mem IS NOT NULL
    GROUP BY bucket, agent_id
)
SELECT bucket::timestamptz                              AS bucket_at,
       COALESCE(round(sum(alloc_cpu)), 0)::bigint       AS allocatable_cpu_millicores,
       COALESCE(round(sum(req_cpu)), 0)::bigint         AS requested_cpu_millicores,
       COALESCE(round(sum(alloc_mem)), 0)::bigint       AS allocatable_memory_bytes,
       COALESCE(round(sum(req_mem)), 0)::bigint         AS requested_memory_bytes,
       count(*)::bigint                                 AS agents
FROM per_agent
GROUP BY bucket
ORDER BY bucket;
