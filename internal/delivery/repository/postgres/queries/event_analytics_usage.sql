-- Event analytics, «Використання»: per participant VPN connection state and
-- lab access over the VPN and the web proxy. Counts and times only, never an
-- address. The moderators team is never counted.

-- name: ListEventUsageUsers :many
-- Every participant of a team (outside the moderators team) with the name an
-- organizer sees. A deleted or purged account keeps an empty name.
SELECT p.user_id,
       t.id                                                                         AS team_id,
       event_team_public_name(t.individual, t.event_id, t.captain_id, t.name)::text AS team_name,
       COALESCE(NULLIF(btrim(concat_ws(' ', u.first_name, u.last_name)), ''), p.pseudonym, '')::text AS user_name,
       presence.last_seen_at                                                        AS last_seen_at,
       COALESCE(lab.at, 'epoch'::timestamptz)::timestamptz                          AS last_lab_at
FROM event_participants p
JOIN event_teams t ON t.id = p.team_id AND NOT t.moderators
LEFT JOIN users u ON u.id = p.user_id
LEFT JOIN event_participant_presence presence ON presence.event_id = p.event_id AND presence.user_id = p.user_id
CROSS JOIN LATERAL (SELECT event_user_last_lab_at(p.event_id, p.user_id) AS at) lab
WHERE p.event_id = sqlc.arg(event_id)
  AND p.status = 2
  AND (sqlc.narg(team_id)::uuid IS NULL OR t.id = sqlc.narg(team_id)::uuid)
ORDER BY t.id, p.user_id;

-- name: ListEventUsageVPN :many
-- VPN sessions per participant over the sessions overlapping the period.
SELECT s.user_id::uuid                                                        AS user_id,
       count(*)::bigint                                                       AS sessions,
       COALESCE(sum(extract(epoch FROM s.ended_at - s.started_at)), 0)::bigint AS seconds,
       COALESCE(sum(s.rx_max - s.rx_min), 0)::bigint                          AS rx_bytes,
       COALESCE(sum(s.tx_max - s.tx_min), 0)::bigint                          AS tx_bytes,
       min(s.started_at)::timestamptz                                         AS first_at,
       max(s.ended_at)::timestamptz                                           AS last_at
FROM event_vpn_sessions s
WHERE s.event_id = sqlc.arg(event_id)
  AND s.user_id IS NOT NULL
  AND s.ended_at >= sqlc.arg(from_at)::timestamptz
  AND s.started_at < sqlc.arg(to_at)::timestamptz
GROUP BY s.user_id;

-- name: ListEventUsageSessions :many
-- The latest sessions of every participant over the period, newest first, at
-- most session_limit per participant.
SELECT x.user_id, x.started_at, x.ended_at, x.rx_bytes, x.tx_bytes
FROM (
    SELECT s.user_id::uuid AS user_id, s.started_at, s.ended_at,
           (s.rx_max - s.rx_min)::bigint AS rx_bytes,
           (s.tx_max - s.tx_min)::bigint AS tx_bytes,
           row_number() OVER (PARTITION BY s.user_id ORDER BY s.started_at DESC) AS n
    FROM event_vpn_sessions s
    WHERE s.event_id = sqlc.arg(event_id)
      AND s.user_id IS NOT NULL
      AND s.ended_at >= sqlc.arg(from_at)::timestamptz
      AND s.started_at < sqlc.arg(to_at)::timestamptz
) x
WHERE x.n <= sqlc.arg(session_limit)::int
ORDER BY x.user_id, x.started_at DESC;

-- name: ListEventUsageLiveHandshakes :many
-- The current last handshake of every peer of the event, from the merged live
-- state (not from the rolled-up sessions, which lag by up to a minute).
SELECT m.event_team_id                                                                   AS team_id,
       COALESCE(c ->> 'name', '')::text                                                   AS client_name,
       COALESCE((c -> 'status' -> 'statistics' ->> 'lastHandshakeUnix')::bigint, 0)::bigint AS handshake_unix,
       m.updated_at
FROM lab_monitoring_current m
CROSS JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(m.payload -> 'clients') = 'array' THEN m.payload -> 'clients' ELSE '[]'::jsonb END) c
WHERE m.event_id = sqlc.arg(event_id);

-- name: ListEventUsageTouches :many
-- Lab access per participant, task and access type (vpn or proxy): the
-- collector's cumulative counters. They carry no time series, so they cover
-- the whole event, not the period.
SELECT t.user_id::uuid                                   AS user_id,
       t.event_challenge_id,
       COALESCE(ec.snapshot ->> 'name', '')::text        AS challenge_name,
       t.surface::text                                   AS surface,
       t.attempts_count,
       t.lab_initiated_attempts_count,
       t.bytes_in,
       t.bytes_out,
       t.first_seen_at,
       t.last_seen_at
FROM event_lab_touches t
LEFT JOIN event_challenges ec ON ec.id = t.event_challenge_id
WHERE t.event_id = sqlc.arg(event_id)
  AND t.user_id IS NOT NULL
ORDER BY t.user_id, challenge_name, t.surface;
