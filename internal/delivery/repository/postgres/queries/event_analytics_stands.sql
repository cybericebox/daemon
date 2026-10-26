-- Event analytics, «Стенди» (docs/EVENT-ANALYTICS.md §6.5). The moderators
-- team is never counted.

-- name: ListEventStandTeams :many
-- Every team of the event with the current status of its stand (0: no stand
-- yet).
SELECT t.id                                                                    AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       t.member_count::bigint                                                  AS member_count,
       COALESCE(s.status, 0)::smallint                                         AS status,
       COALESCE(s.reason, '')::text                                            AS reason,
       COALESCE(s.generation, 0)::integer                                      AS generation,
       s.status_changed_at
FROM event_teams t
LEFT JOIN event_team_stands s ON s.event_team_id = t.id
WHERE t.event_id = sqlc.arg(event_id)
  AND NOT t.moderators
ORDER BY 2, t.id;

-- name: ListEventStandTransitions :many
-- The status log of stands and task labs, oldest first. A lab row carries the
-- task it belongs to.
SELECT x.team_id,
       x.source,
       x.challenge_id,
       COALESCE(c.snapshot ->> 'name', '')::text AS challenge_name,
       x.generation,
       COALESCE(x.from_status, -1)::smallint     AS from_status,
       x.to_status,
       COALESCE(x.reason, '')::text              AS reason,
       x.at
FROM event_stand_transitions x
JOIN event_teams t ON t.id = x.team_id AND NOT t.moderators
LEFT JOIN event_challenges c ON c.id = x.challenge_id
WHERE x.event_id = sqlc.arg(event_id)
ORDER BY x.at, x.id;

-- name: ListEventStandResources :many
-- Resource usage per team from the lab telemetry (the minute buckets of
-- event_lab_usage_minutes). A device is one container of one lab; its peak is
-- over the buckets in the period, and a team's figure is the sum of its
-- devices' peaks (observations can be partial, so a sum inside one
-- observation would understate). Restarts are the highest restart count seen
-- per device. Devices without a usage reading count only toward devices and
-- restarts.
WITH per_device AS (
    SELECT u.team_id, u.lab_name, u.device_name,
           max(u.cpu_max) AS peak_cpu, max(u.memory_max) AS peak_memory, max(u.restart_max) AS restarts
    FROM event_lab_usage_minutes u
    JOIN event_teams t ON t.id = u.team_id AND NOT t.moderators
    WHERE u.event_id = sqlc.arg(event_id)
      AND u.bucket_at >= date_trunc('minute', sqlc.arg(from_at)::timestamptz)
      AND u.bucket_at < sqlc.arg(to_at)::timestamptz
    GROUP BY u.team_id, u.lab_name, u.device_name
)
SELECT team_id,
       count(*)::bigint                         AS devices,
       COALESCE(sum(peak_cpu), 0)::bigint       AS peak_cpu_millicores,
       COALESCE(sum(peak_memory), 0)::bigint    AS peak_memory_bytes,
       COALESCE(sum(restarts), 0)::bigint       AS restarts,
       count(*) FILTER (WHERE restarts > 0)::bigint AS restarted_devices
FROM per_device
GROUP BY team_id;

-- name: ListEventVPNUsage :many
-- VPN usage per team over the sessions overlapping the period. Users are the
-- distinct clients (participants) that connected; a session with a single
-- handshake lasts zero seconds.
SELECT s.team_id,
       count(*)::bigint                                                                          AS sessions,
       count(DISTINCT COALESCE(s.user_id::text, s.client_name))::bigint                           AS users,
       COALESCE(sum(extract(epoch FROM s.ended_at - s.started_at)), 0)::bigint                    AS seconds,
       COALESCE(sum(s.rx_max - s.rx_min), 0)::bigint                                             AS rx_bytes,
       COALESCE(sum(s.tx_max - s.tx_min), 0)::bigint                                             AS tx_bytes,
       min(s.started_at)::timestamptz                                                            AS first_at,
       max(s.ended_at)::timestamptz                                                              AS last_at
FROM event_vpn_sessions s
JOIN event_teams t ON t.id = s.team_id AND NOT t.moderators
WHERE s.event_id = sqlc.arg(event_id)
  AND s.ended_at >= sqlc.arg(from_at)::timestamptz
  AND s.started_at < sqlc.arg(to_at)::timestamptz
GROUP BY s.team_id;
