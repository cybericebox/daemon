-- Reads of the lab telemetry rollups for the event analytics API: one team,
-- one time range. The moderators team is never listed.

-- name: ListEventTeamLabUsageMinutes :many
-- Per device and minute: samples, CPU (millicores) and memory (bytes) with
-- averages from the sums, restarts, ready samples, last pod phase.
SELECT m.lab_group_name, m.lab_name, m.device_name, m.bucket_at, m.samples,
       m.cpu_samples, m.cpu_sum, m.cpu_max, m.memory_samples, m.memory_sum, m.memory_max,
       m.restart_max, m.ready_samples, m.last_pod_phase
FROM event_lab_usage_minutes m
JOIN event_teams t ON t.id = m.team_id AND t.event_id = m.event_id AND NOT t.moderators
WHERE m.event_id = sqlc.arg(event_id)
  AND m.team_id = sqlc.arg(team_id)
  AND m.bucket_at >= sqlc.arg(from_at)::timestamptz
  AND m.bucket_at < sqlc.arg(to_at)::timestamptz
ORDER BY m.bucket_at, m.lab_name, m.device_name
LIMIT sqlc.arg(row_limit);

-- name: ListEventTeamLabMinutes :many
SELECT m.lab_group_name, m.lab_name, m.bucket_at, m.samples, m.ready_samples, m.vpn_ready_samples
FROM event_lab_minutes m
JOIN event_teams t ON t.id = m.team_id AND t.event_id = m.event_id AND NOT t.moderators
WHERE m.event_id = sqlc.arg(event_id)
  AND m.team_id = sqlc.arg(team_id)
  AND m.bucket_at >= sqlc.arg(from_at)::timestamptz
  AND m.bucket_at < sqlc.arg(to_at)::timestamptz
ORDER BY m.bucket_at, m.lab_name
LIMIT sqlc.arg(row_limit);

-- name: ListEventTeamVPNClientMinutes :many
-- Per VPN client (participant) and minute: traffic of the minute and the
-- samples that saw the peer connected.
SELECT m.lab_group_name, m.client_name, m.bucket_at, m.samples, m.connected_samples, m.rx_bytes, m.tx_bytes
FROM event_vpn_client_minutes m
JOIN event_teams t ON t.id = m.team_id AND t.event_id = m.event_id AND NOT t.moderators
WHERE m.event_id = sqlc.arg(event_id)
  AND m.team_id = sqlc.arg(team_id)
  AND m.bucket_at >= sqlc.arg(from_at)::timestamptz
  AND m.bucket_at < sqlc.arg(to_at)::timestamptz
ORDER BY m.bucket_at, m.client_name
LIMIT sqlc.arg(row_limit);

-- name: ListEventTeamLabTransitions :many
-- The transitions journal of a team in a time range (agent time), oldest
-- first; kinds narrows to some kinds, empty means all. For lab_ready it also
-- gives the platform's own time of marking that lab ready (the first
-- lab_bindings readiness change to Ready at or after 30 s before what the
-- agent saw), so the lag between the agent seeing Ready and the platform
-- acting on it can be read: platform_ready_at - at.
SELECT x.id, x.lab_group_name, x.lab_name, x.device_name, x.client_name, x.kind, x.at, x.received_at, x.detail,
       st.at AS platform_ready_at
FROM event_lab_transitions x
LEFT JOIN lab_bindings b
       ON x.kind = 'lab_ready' AND b.event_team_id = x.team_id AND b.lab_group_name = x.lab_group_name
          AND b.lab_name = x.lab_name
LEFT JOIN event_stand_transitions st
       ON st.event_id = b.event_id AND st.team_id = b.event_team_id AND st.source = 'lab'
          AND st.challenge_id = b.event_challenge_id AND st.to_status = 1
          AND st.at >= x.at - interval '30 seconds'
          AND NOT EXISTS (SELECT 1
                          FROM event_stand_transitions earlier
                          WHERE earlier.event_id = st.event_id AND earlier.team_id = st.team_id AND earlier.source = 'lab'
                            AND earlier.challenge_id = st.challenge_id AND earlier.to_status = 1
                            AND earlier.at >= x.at - interval '30 seconds' AND earlier.at < st.at)
JOIN event_teams t ON t.id = x.team_id AND t.event_id = x.event_id AND NOT t.moderators
WHERE x.event_id = sqlc.arg(event_id)
  AND x.team_id = sqlc.arg(team_id)
  AND x.at >= sqlc.arg(from_at)::timestamptz
  AND x.at < sqlc.arg(to_at)::timestamptz
  AND (cardinality(sqlc.arg(kinds)::text[]) = 0 OR x.kind = ANY (sqlc.arg(kinds)::text[]))
ORDER BY x.at, x.id
LIMIT sqlc.arg(row_limit);
