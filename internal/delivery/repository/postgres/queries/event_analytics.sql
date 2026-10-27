-- Event analytics (docs/EVENT-ANALYTICS.md): the rollup job's statements and
-- the reads of the analytics API. The moderators team is never counted.

-- name: ListEventAnalyticsRollupCandidates :many
-- Events the rollup job still has work for: started, and without finalized
-- buckets (or with results changed since), or with a VPN or lab usage rollup
-- still open (infrastructure events), or with raw observations not archived
-- yet (archive_before: only closed hours are archived; started or not).
-- Running events come first. A closed VPN or lab usage rollup is open again
-- while observations arrived after its cursor (a pass that closed it before
-- the telemetry existed, or rows that arrived late); vpn_closed /
-- lab_usage_closed already account for that (the lab usage rollup covers the
-- minute buckets and the transitions journal). not_started marks an event listed
-- only for its archive: buckets and the rollups do not apply to it yet.
SELECT e.id,
       e.start_at,
       (LEAST(e.manual_finished_at, e.finish_at) IS NOT NULL)::boolean AS has_finish,
       COALESCE(LEAST(e.manual_finished_at, e.finish_at), e.start_at)::timestamptz AS finished_at,
       e.infrastructure_allowed,
       (NOT (e.lifecycle_configured AND e.start_at <= sqlc.arg(now)::timestamptz))::boolean AS not_started,
       COALESCE(r.revision, 0)::bigint                       AS revision,
       COALESCE(s.buckets_revision, 0)::bigint                AS buckets_revision,
       s.buckets_finalized_at,
       (s.vpn_finalized_at IS NOT NULL AND NOT pending.vpn)::boolean AS vpn_closed,
       s.vpn_cursor_received_at,
       s.vpn_cursor_id,
       (s.lab_usage_finalized_at IS NOT NULL AND NOT pending.lab_usage)::boolean AS lab_usage_closed,
       s.lab_usage_cursor_received_at,
       s.lab_usage_cursor_id,
       s.lab_journal_cursor_received_at,
       s.lab_journal_cursor_id,
       (s.lab_telemetry_hold_until IS NOT NULL AND s.lab_telemetry_hold_until > sqlc.arg(now)::timestamptz)::boolean AS lab_held,
       pending.archive::boolean                              AS archive_open,
       s.lab_archive_cursor_received_at,
       s.lab_archive_cursor_id
FROM events e
LEFT JOIN event_result_revisions r ON r.event_id = e.id
LEFT JOIN event_analytics_rollups s ON s.event_id = e.id
CROSS JOIN LATERAL (
    SELECT e.infrastructure_allowed AND EXISTS (
               SELECT 1 FROM event_lab_observations o
               WHERE o.event_id = e.id
                 AND (o.received_at, o.id) > (COALESCE(s.vpn_cursor_received_at, 'epoch'::timestamptz),
                                              COALESCE(s.vpn_cursor_id, '00000000-0000-0000-0000-000000000000'::uuid))) AS vpn,
           e.infrastructure_allowed AND (EXISTS (
               SELECT 1 FROM event_lab_observations o
               WHERE o.event_id = e.id
                 AND (o.received_at, o.id) > (COALESCE(s.lab_usage_cursor_received_at, 'epoch'::timestamptz),
                                              COALESCE(s.lab_usage_cursor_id, '00000000-0000-0000-0000-000000000000'::uuid)))
               OR EXISTS (
               SELECT 1 FROM event_lab_observations o
               WHERE o.event_id = e.id
                 AND (o.received_at, o.id) > (COALESCE(s.lab_journal_cursor_received_at, 'epoch'::timestamptz),
                                              COALESCE(s.lab_journal_cursor_id, '00000000-0000-0000-0000-000000000000'::uuid)))) AS lab_usage,
           EXISTS (
               SELECT 1 FROM event_lab_observations o
               WHERE o.event_id = e.id
                 AND o.received_at < sqlc.arg(archive_before)::timestamptz
                 AND (o.received_at, o.id) > (COALESCE(s.lab_archive_cursor_received_at, 'epoch'::timestamptz),
                                              COALESCE(s.lab_archive_cursor_id, '00000000-0000-0000-0000-000000000000'::uuid))) AS archive
) pending
WHERE pending.archive
   OR (e.lifecycle_configured
       AND e.start_at <= sqlc.arg(now)::timestamptz
       AND (s.event_id IS NULL
         OR s.buckets_finalized_at IS NULL
         OR s.buckets_revision <> COALESCE(r.revision, 0)
         OR (e.infrastructure_allowed AND (s.vpn_finalized_at IS NULL OR pending.vpn))
         OR (e.infrastructure_allowed AND (s.lab_usage_finalized_at IS NULL OR pending.lab_usage))))
ORDER BY (LEAST(e.manual_finished_at, e.finish_at) IS NULL
    OR LEAST(e.manual_finished_at, e.finish_at) > sqlc.arg(now)::timestamptz) DESC,
         e.start_at DESC, e.id
LIMIT sqlc.arg(row_limit);

-- name: RefreshEventActivityBuckets :exec
-- Rebuilds an event's 5-minute buckets from the sources: attempts (and the
-- effectively correct ones, after manual decisions), solves and task opens.
-- Upserts the fresh buckets and removes the ones no longer backed by data, in
-- one statement, so a re-run is a no-op.
WITH teams AS (
    SELECT t.id FROM event_teams t WHERE t.event_id = sqlc.arg(event_id) AND NOT t.moderators
), facts AS (
    SELECT date_bin('5 minutes', a.received_at, 'epoch'::timestamptz) AS bucket_at, a.event_team_id AS team_id,
           tc.event_challenge_id AS challenge_id, 1 AS attempts, a.effective_correct::int AS correct, 0 AS solves, 0 AS opens
    FROM effective_challenge_attempts a
    JOIN teams ON teams.id = a.event_team_id
    JOIN team_challenges tc ON tc.id = a.team_challenge_id
    WHERE a.event_id = sqlc.arg(event_id)
    UNION ALL
    SELECT date_bin('5 minutes', s.solved_at, 'epoch'::timestamptz), tc.event_team_id, tc.event_challenge_id, 0, 0, 1, 0
    FROM team_challenge_solves s
    JOIN team_challenges tc ON tc.id = s.team_challenge_id
    JOIN teams ON teams.id = tc.event_team_id
    WHERE tc.event_id = sqlc.arg(event_id)
    UNION ALL
    SELECT date_bin('5 minutes', o.at, 'epoch'::timestamptz), o.team_id, o.subject_id, 0, 0, 0, 1
    FROM event_activity o
    JOIN teams ON teams.id = o.team_id
    WHERE o.event_id = sqlc.arg(event_id)
      AND o.kind = 'task_opened'
      AND o.subject_id IS NOT NULL
), fresh AS (
    SELECT bucket_at, team_id, challenge_id,
           sum(attempts)::int AS attempts, sum(correct)::int AS correct, sum(solves)::int AS solves, sum(opens)::int AS opens
    FROM facts
    GROUP BY bucket_at, team_id, challenge_id
), upserted AS (
    INSERT INTO event_activity_buckets (event_id, bucket_at, team_id, challenge_id, attempts, correct, solves, opens)
    SELECT sqlc.arg(event_id), bucket_at, team_id, challenge_id, attempts, correct, solves, opens
    FROM fresh
    ON CONFLICT (event_id, bucket_at, team_id, challenge_id) DO UPDATE
        SET attempts = EXCLUDED.attempts, correct = EXCLUDED.correct, solves = EXCLUDED.solves, opens = EXCLUDED.opens
    RETURNING 1
)
DELETE FROM event_activity_buckets b
WHERE b.event_id = sqlc.arg(event_id)
  AND NOT EXISTS (SELECT 1
                  FROM fresh f
                  WHERE f.bucket_at = b.bucket_at AND f.team_id = b.team_id AND f.challenge_id = b.challenge_id);

-- name: MarkEventActivityBucketsRefreshed :exec
INSERT INTO event_analytics_rollups (event_id, buckets_refreshed_at, buckets_revision, buckets_finalized_at)
VALUES (sqlc.arg(event_id), sqlc.arg(refreshed_at), sqlc.arg(revision), sqlc.narg(finalized_at))
ON CONFLICT (event_id) DO UPDATE
    SET buckets_refreshed_at = EXCLUDED.buckets_refreshed_at,
        buckets_revision     = EXCLUDED.buckets_revision,
        buckets_finalized_at = EXCLUDED.buckets_finalized_at;

-- name: ListEventVPNSamples :many
-- The WireGuard peer statistics of up to batch_size observations after the
-- cursor, in arrival order. An observation without clients yields one row
-- with an empty client name, so the cursor still moves past it.
WITH batch AS (
    SELECT o.id, o.received_at, o.event_team_id, o.observed_at, o.payload
    FROM event_lab_observations o
    WHERE o.event_id = sqlc.arg(event_id)
      AND (o.received_at, o.id) > (sqlc.arg(after_received_at)::timestamptz, sqlc.arg(after_id)::uuid)
    ORDER BY o.received_at, o.id
    LIMIT sqlc.arg(batch_size)
)
SELECT b.id                                                                          AS observation_id,
       b.received_at,
       b.event_team_id                                                               AS team_id,
       b.observed_at,
       COALESCE(c ->> 'name', '')::text                                              AS client_name,
       COALESCE(lab_telemetry_number(c -> 'status' -> 'statistics' ->> 'lastHandshakeUnix'), 0)::bigint AS handshake_unix,
       COALESCE(lab_telemetry_number(c -> 'status' -> 'statistics' ->> 'rxBytes'), 0)::bigint   AS rx_bytes,
       COALESCE(lab_telemetry_number(c -> 'status' -> 'statistics' ->> 'txBytes'), 0)::bigint   AS tx_bytes
FROM batch b
LEFT JOIN LATERAL jsonb_array_elements(
        CASE WHEN jsonb_typeof(b.payload -> 'clients') = 'array' THEN b.payload -> 'clients' ELSE '[]'::jsonb END) c ON true
ORDER BY b.received_at, b.id;

-- name: ListOpenEventVPNSessions :many
-- Sessions a new handshake may still extend: those ending at or after
-- ended_after.
SELECT id, event_id, team_id, user_id, client_name, started_at, ended_at, rx_min, rx_max, tx_min, tx_max
FROM event_vpn_sessions
WHERE event_id = sqlc.arg(event_id)
  AND ended_at >= sqlc.arg(ended_after)
ORDER BY started_at, id;

-- name: UpsertEventVPNSession :exec
INSERT INTO event_vpn_sessions (id, event_id, team_id, user_id, client_name, started_at, ended_at, rx_min, rx_max, tx_min, tx_max)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(team_id), sqlc.narg(user_id), sqlc.arg(client_name), sqlc.arg(started_at),
        sqlc.arg(ended_at), sqlc.arg(rx_min), sqlc.arg(rx_max), sqlc.arg(tx_min), sqlc.arg(tx_max))
ON CONFLICT (id) DO UPDATE
    SET started_at = EXCLUDED.started_at,
        ended_at   = EXCLUDED.ended_at,
        rx_min     = EXCLUDED.rx_min,
        rx_max     = EXCLUDED.rx_max,
        tx_min     = EXCLUDED.tx_min,
        tx_max     = EXCLUDED.tx_max;

-- name: DeleteEventVPNSessions :exec
DELETE FROM event_vpn_sessions
WHERE event_id = sqlc.arg(event_id)
  AND id = ANY (sqlc.arg(ids)::uuid[]);

-- name: SetEventVPNCursor :exec
INSERT INTO event_analytics_rollups (event_id, vpn_cursor_received_at, vpn_cursor_id, vpn_finalized_at)
VALUES (sqlc.arg(event_id), sqlc.narg(cursor_received_at), sqlc.narg(cursor_id), sqlc.narg(finalized_at))
ON CONFLICT (event_id) DO UPDATE
    SET vpn_cursor_received_at = EXCLUDED.vpn_cursor_received_at,
        vpn_cursor_id          = EXCLUDED.vpn_cursor_id,
        vpn_finalized_at       = EXCLUDED.vpn_finalized_at;

-- name: RollupEventLabUsage :one
-- Folds up to batch_size observations after the cursor, in arrival order, into
-- the minute buckets (per device, per lab, per VPN client) and moves the
-- cursor past them, in one statement: a failure leaves both untouched, so a
-- re-run never counts an observation twice. A minute spanning two batches
-- merges: counts and sums add up, maxima take the larger, "last" values the
-- newer one. The traffic of a client is the growth of its counters between
-- consecutive samples (continuing from the last sample stored before the
-- batch); a counter that went down is a reset and counts the new value; the
-- first sample of a client has no baseline and counts 0. A client is
-- connected when its last handshake is at most 3 minutes older than the
-- sample. Returns how many observations it read (fewer than batch_size:
-- caught up) and the cursor after the last one (the given cursor when it
-- read none).
WITH batch AS (
    SELECT o.id, o.received_at, o.event_team_id, o.lab_group_name, o.observed_at, o.payload
    FROM event_lab_observations o
    WHERE o.event_id = sqlc.arg(event_id)
      AND (o.received_at, o.id) > (sqlc.arg(after_received_at)::timestamptz, sqlc.arg(after_id)::uuid)
    ORDER BY o.received_at, o.id
    LIMIT sqlc.arg(batch_size)
), last AS (
    SELECT b.received_at, b.id FROM batch b ORDER BY b.received_at DESC, b.id DESC LIMIT 1
), lab_rows AS (
    SELECT b.id, b.event_team_id AS team_id, b.lab_group_name, b.observed_at, lab
    FROM batch b
    CROSS JOIN LATERAL jsonb_array_elements(
            CASE WHEN jsonb_typeof(b.payload -> 'labs') = 'array' THEN b.payload -> 'labs' ELSE '[]'::jsonb END) lab
), devices AS (
    SELECT l.team_id,
           l.lab_group_name,
           COALESCE(l.lab ->> 'name', '')                                                    AS lab_name,
           COALESCE(dev ->> 'name', '')                                                      AS device_name,
           date_trunc('minute', l.observed_at)                                               AS bucket_at,
           l.observed_at,
           l.id,
           CASE WHEN dev ->> 'usageAvailable' = 'true' THEN lab_telemetry_number(dev ->> 'cpuMillicores') END AS cpu,
           CASE WHEN dev ->> 'usageAvailable' = 'true' THEN lab_telemetry_number(dev ->> 'memoryBytes') END   AS memory,
           COALESCE(lab_telemetry_number(dev ->> 'restartCount'), 0)                           AS restarts,
           COALESCE(dev ->> 'ready' = 'true', false)                                         AS ready,
           NULLIF(dev ->> 'podPhase', '')                                                    AS pod_phase
    FROM lab_rows l
    CROSS JOIN LATERAL jsonb_array_elements(
            CASE WHEN jsonb_typeof(l.lab -> 'status' -> 'devices') = 'array' THEN l.lab -> 'status' -> 'devices' ELSE '[]'::jsonb END) dev
), fresh AS (
    SELECT team_id, lab_group_name, lab_name, device_name, bucket_at,
           count(*)::integer                  AS samples,
           count(cpu)::integer                AS cpu_samples,
           COALESCE(sum(cpu), 0)::bigint      AS cpu_sum,
           max(cpu)                           AS cpu_max,
           max(memory)                        AS memory_max,
           max(restarts)                      AS restart_max,
           COALESCE(sum(memory), 0)::bigint   AS memory_sum,
           count(memory)::integer             AS memory_samples,
           (count(*) FILTER (WHERE ready))::integer AS ready_samples,
           (array_agg(pod_phase ORDER BY observed_at DESC, id DESC) FILTER (WHERE pod_phase IS NOT NULL))[1] AS last_pod_phase,
           max(observed_at) FILTER (WHERE pod_phase IS NOT NULL) AS last_phase_at
    FROM devices
    GROUP BY team_id, lab_group_name, lab_name, device_name, bucket_at
), upserted AS (
    INSERT INTO event_lab_usage_minutes (event_id, team_id, lab_group_name, lab_name, device_name, bucket_at,
                                         samples, cpu_samples, cpu_sum, cpu_max, memory_max, restart_max,
                                         memory_sum, memory_samples, ready_samples, last_pod_phase, last_phase_at)
    SELECT sqlc.arg(event_id), team_id, lab_group_name, lab_name, device_name, bucket_at,
           samples, cpu_samples, cpu_sum, cpu_max, memory_max, restart_max,
           memory_sum, memory_samples, ready_samples, last_pod_phase, last_phase_at
    FROM fresh
    ON CONFLICT (event_id, team_id, lab_group_name, lab_name, device_name, bucket_at) DO UPDATE
        SET samples        = event_lab_usage_minutes.samples + EXCLUDED.samples,
            cpu_samples    = event_lab_usage_minutes.cpu_samples + EXCLUDED.cpu_samples,
            cpu_sum        = event_lab_usage_minutes.cpu_sum + EXCLUDED.cpu_sum,
            cpu_max        = GREATEST(event_lab_usage_minutes.cpu_max, EXCLUDED.cpu_max),
            memory_max     = GREATEST(event_lab_usage_minutes.memory_max, EXCLUDED.memory_max),
            restart_max    = GREATEST(event_lab_usage_minutes.restart_max, EXCLUDED.restart_max),
            memory_sum     = event_lab_usage_minutes.memory_sum + EXCLUDED.memory_sum,
            memory_samples = event_lab_usage_minutes.memory_samples + EXCLUDED.memory_samples,
            ready_samples  = event_lab_usage_minutes.ready_samples + EXCLUDED.ready_samples,
            last_pod_phase = CASE WHEN EXCLUDED.last_phase_at IS NOT NULL
                                       AND (event_lab_usage_minutes.last_phase_at IS NULL
                                            OR EXCLUDED.last_phase_at >= event_lab_usage_minutes.last_phase_at)
                                  THEN EXCLUDED.last_pod_phase ELSE event_lab_usage_minutes.last_pod_phase END,
            last_phase_at  = GREATEST(event_lab_usage_minutes.last_phase_at, EXCLUDED.last_phase_at)
    RETURNING 1
), fresh_labs AS (
    SELECT l.team_id, l.lab_group_name, COALESCE(l.lab ->> 'name', '') AS lab_name,
           date_trunc('minute', l.observed_at)                          AS bucket_at,
           count(*)::integer                                            AS samples,
           (count(*) FILTER (WHERE l.lab -> 'status' ->> 'ready' = 'true'))::integer    AS ready_samples,
           (count(*) FILTER (WHERE l.lab -> 'status' ->> 'vpnReady' = 'true'))::integer AS vpn_ready_samples
    FROM lab_rows l
    GROUP BY l.team_id, l.lab_group_name, COALESCE(l.lab ->> 'name', ''), date_trunc('minute', l.observed_at)
), upserted_labs AS (
    INSERT INTO event_lab_minutes (event_id, team_id, lab_group_name, lab_name, bucket_at,
                                   samples, ready_samples, vpn_ready_samples)
    SELECT sqlc.arg(event_id), team_id, lab_group_name, lab_name, bucket_at, samples, ready_samples, vpn_ready_samples
    FROM fresh_labs
    ON CONFLICT (event_id, team_id, lab_group_name, lab_name, bucket_at) DO UPDATE
        SET samples           = event_lab_minutes.samples + EXCLUDED.samples,
            ready_samples     = event_lab_minutes.ready_samples + EXCLUDED.ready_samples,
            vpn_ready_samples = event_lab_minutes.vpn_ready_samples + EXCLUDED.vpn_ready_samples
    RETURNING 1
), client_samples AS (
    SELECT b.id, b.event_team_id AS team_id, b.lab_group_name, b.observed_at,
           COALESCE(c ->> 'name', '')                                                       AS client_name,
           COALESCE(lab_telemetry_number(c -> 'status' -> 'statistics' ->> 'rxBytes'), 0)           AS rx,
           COALESCE(lab_telemetry_number(c -> 'status' -> 'statistics' ->> 'txBytes'), 0)           AS tx,
           COALESCE(lab_telemetry_number(c -> 'status' -> 'statistics' ->> 'lastHandshakeUnix'), 0) AS handshake
    FROM batch b
    CROSS JOIN LATERAL jsonb_array_elements(
            CASE WHEN jsonb_typeof(b.payload -> 'clients') = 'array' THEN b.payload -> 'clients' ELSE '[]'::jsonb END) c
    WHERE COALESCE(c ->> 'name', '') <> ''
), previous AS (
    SELECT DISTINCT ON (m.team_id, m.lab_group_name, m.client_name)
           m.team_id, m.lab_group_name, m.client_name, m.last_rx, m.last_tx
    FROM event_vpn_client_minutes m
    WHERE m.event_id = sqlc.arg(event_id)
      AND (m.team_id, m.lab_group_name, m.client_name) IN (SELECT cs.team_id, cs.lab_group_name, cs.client_name FROM client_samples cs)
    ORDER BY m.team_id, m.lab_group_name, m.client_name, m.bucket_at DESC
), client_steps AS (
    SELECT cs.*,
           COALESCE(lag(cs.rx) OVER w, p.last_rx) AS prev_rx,
           COALESCE(lag(cs.tx) OVER w, p.last_tx) AS prev_tx
    FROM client_samples cs
    LEFT JOIN previous p ON p.team_id = cs.team_id AND p.lab_group_name = cs.lab_group_name AND p.client_name = cs.client_name
    WINDOW w AS (PARTITION BY cs.team_id, cs.lab_group_name, cs.client_name ORDER BY cs.observed_at, cs.id)
), client_deltas AS (
    SELECT team_id, lab_group_name, client_name, observed_at, id, rx, tx,
           date_trunc('minute', observed_at) AS bucket_at,
           CASE WHEN prev_rx IS NULL THEN 0 WHEN rx >= prev_rx THEN rx - prev_rx ELSE rx END AS rx_delta,
           CASE WHEN prev_tx IS NULL THEN 0 WHEN tx >= prev_tx THEN tx - prev_tx ELSE tx END AS tx_delta,
           (handshake BETWEEN 1 AND 4102444800 AND observed_at - to_timestamp(handshake) <= interval '3 minutes') AS connected
    FROM client_steps
), fresh_clients AS (
    SELECT team_id, lab_group_name, client_name, bucket_at,
           count(*)::integer                                  AS samples,
           (count(*) FILTER (WHERE connected))::integer       AS connected_samples,
           COALESCE(sum(rx_delta), 0)::bigint                 AS rx_bytes,
           COALESCE(sum(tx_delta), 0)::bigint                 AS tx_bytes,
           (array_agg(rx ORDER BY observed_at DESC, id DESC))[1] AS last_rx,
           (array_agg(tx ORDER BY observed_at DESC, id DESC))[1] AS last_tx,
           max(observed_at)                                   AS last_observed_at
    FROM client_deltas
    GROUP BY team_id, lab_group_name, client_name, bucket_at
), upserted_clients AS (
    INSERT INTO event_vpn_client_minutes (event_id, team_id, lab_group_name, client_name, bucket_at,
                                          samples, connected_samples, rx_bytes, tx_bytes, last_rx, last_tx, last_observed_at)
    SELECT sqlc.arg(event_id), team_id, lab_group_name, client_name, bucket_at,
           samples, connected_samples, rx_bytes, tx_bytes, last_rx, last_tx, last_observed_at
    FROM fresh_clients
    ON CONFLICT (event_id, team_id, lab_group_name, client_name, bucket_at) DO UPDATE
        SET samples           = event_vpn_client_minutes.samples + EXCLUDED.samples,
            connected_samples = event_vpn_client_minutes.connected_samples + EXCLUDED.connected_samples,
            rx_bytes          = event_vpn_client_minutes.rx_bytes + EXCLUDED.rx_bytes,
            tx_bytes          = event_vpn_client_minutes.tx_bytes + EXCLUDED.tx_bytes,
            last_rx           = CASE WHEN EXCLUDED.last_observed_at >= event_vpn_client_minutes.last_observed_at
                                     THEN EXCLUDED.last_rx ELSE event_vpn_client_minutes.last_rx END,
            last_tx           = CASE WHEN EXCLUDED.last_observed_at >= event_vpn_client_minutes.last_observed_at
                                     THEN EXCLUDED.last_tx ELSE event_vpn_client_minutes.last_tx END,
            last_observed_at  = GREATEST(event_vpn_client_minutes.last_observed_at, EXCLUDED.last_observed_at)
    RETURNING 1
), moved AS (
    INSERT INTO event_analytics_rollups (event_id, lab_usage_cursor_received_at, lab_usage_cursor_id)
    SELECT sqlc.arg(event_id), l.received_at, l.id FROM last l
    ON CONFLICT (event_id) DO UPDATE
        SET lab_usage_cursor_received_at = EXCLUDED.lab_usage_cursor_received_at,
            lab_usage_cursor_id          = EXCLUDED.lab_usage_cursor_id,
            lab_usage_finalized_at       = NULL
    RETURNING 1
)
SELECT (SELECT count(*) FROM batch)::bigint                 AS observations,
       COALESCE((SELECT l.received_at FROM last l), sqlc.arg(after_received_at)::timestamptz)::timestamptz AS cursor_received_at,
       COALESCE((SELECT l.id FROM last l), sqlc.arg(after_id)::uuid)::uuid AS cursor_id;

-- name: FinishEventLabUsageRollup :exec
INSERT INTO event_analytics_rollups (event_id, lab_usage_finalized_at)
VALUES (sqlc.arg(event_id), sqlc.arg(finalized_at))
ON CONFLICT (event_id) DO UPDATE
    SET lab_usage_finalized_at = EXCLUDED.lab_usage_finalized_at;

-- name: GetEventAnalyticsOverview :one
-- The overview counters (§6.1). Registered excludes pending invitations;
-- active are the distinct participants with an attempt or a task open or
-- download since active_since.
WITH teams AS (
    SELECT t.id,
           event_team_admitted(t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count) AS admitted
    FROM event_teams t
    WHERE t.event_id = sqlc.arg(event_id)
      AND NOT t.moderators
), participants AS (
    SELECT p.user_id, p.status, p.invited
    FROM event_participants p
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE p.event_id = sqlc.arg(event_id)
      AND NOT COALESCE(t.moderators, false)
), attempts AS (
    SELECT a.user_id, a.effective_correct, a.received_at
    FROM effective_challenge_attempts a
    JOIN teams ON teams.id = a.event_team_id
    WHERE a.event_id = sqlc.arg(event_id)
), stands AS (
    SELECT s.status
    FROM event_team_stands s
    JOIN teams ON teams.id = s.event_team_id
    WHERE s.event_id = sqlc.arg(event_id)
)
SELECT (SELECT count(*) FROM participants WHERE NOT (invited AND status = 1))::bigint   AS participants_registered,
       (SELECT count(*) FROM participants WHERE status = 2)::bigint                     AS participants_approved,
       (SELECT count(*) FROM participants WHERE status = 1 AND NOT invited)::bigint     AS participants_pending,
       (SELECT count(*) FROM participants WHERE status = 1 AND invited)::bigint         AS participants_invited,
       (SELECT count(DISTINCT x.user_id)
        FROM (SELECT user_id FROM attempts WHERE received_at >= sqlc.arg(active_since)::timestamptz
              UNION ALL
              SELECT o.user_id
              FROM event_activity o
              JOIN teams ON teams.id = o.team_id
              WHERE o.event_id = sqlc.arg(event_id)
                AND o.kind IN ('task_opened', 'attachment_downloaded')
                AND o.at >= sqlc.arg(active_since)::timestamptz
                AND o.user_id IS NOT NULL) x)::bigint                                  AS participants_active,
       (SELECT count(*) FROM teams)::bigint                                             AS teams_total,
       (SELECT count(*) FROM teams WHERE admitted)::bigint                              AS teams_admitted,
       (SELECT count(*) FROM attempts)::bigint                                          AS attempts,
       (SELECT count(*) FROM attempts WHERE effective_correct)::bigint                  AS attempts_correct,
       (SELECT count(*)
        FROM team_challenge_solves s
        JOIN team_challenges tc ON tc.id = s.team_challenge_id
        JOIN teams ON teams.id = tc.event_team_id
        WHERE tc.event_id = sqlc.arg(event_id))::bigint                                 AS solves,
       (SELECT count(*)
        FROM team_challenge_hint_unlocks h
        JOIN teams ON teams.id = h.event_team_id
        WHERE h.event_id = sqlc.arg(event_id))::bigint                                  AS hints_opened,
       (SELECT COALESCE(sum(h.cost), 0)
        FROM team_challenge_hint_unlocks h
        JOIN teams ON teams.id = h.event_team_id
        WHERE h.event_id = sqlc.arg(event_id))::bigint                                  AS hint_points,
       (SELECT count(*) FROM stands WHERE status = 1)::bigint                           AS stands_creating,
       (SELECT count(*) FROM stands WHERE status = 2)::bigint                           AS stands_ready,
       (SELECT count(*) FROM stands WHERE status = 3)::bigint                           AS stands_failed;

-- name: ListEventActivitySeries :many
-- The event-wide 5-minute series in [from_at, to_at).
SELECT bucket_at,
       sum(attempts)::bigint AS attempts,
       sum(correct)::bigint  AS correct,
       sum(solves)::bigint   AS solves,
       sum(opens)::bigint    AS opens
FROM event_activity_buckets
WHERE event_id = sqlc.arg(event_id)
  AND bucket_at >= sqlc.arg(from_at)::timestamptz
  AND bucket_at < sqlc.arg(to_at)::timestamptz
GROUP BY bucket_at
ORDER BY bucket_at;

-- name: GetEventAnalyticsRollupState :one
SELECT buckets_refreshed_at, buckets_finalized_at
FROM event_analytics_rollups
WHERE event_id = sqlc.arg(event_id);

-- name: ListEventAnalyticsFeed :many
-- The newest notable moments of an event (§6.1): first bloods (the first
-- solve of a challenge among the ranked teams), stand failures and created
-- teams (individual teams are one per participant and are not news). The
-- moderators team is never listed.
WITH first_bloods AS (
    SELECT 'first_blood'::text AS kind, ranked.solved_at::timestamptz AS occurred_at, ranked.event_team_id::uuid AS team_id,
           team.name::text AS team_name, ranked.event_challenge_id::uuid AS challenge_id,
           COALESCE(challenge.snapshot->>'name', '')::text AS challenge_name, ''::text AS detail
    FROM (
        SELECT tc.event_team_id, tc.event_challenge_id, s.solved_at,
               row_number() OVER (PARTITION BY tc.event_challenge_id ORDER BY s.solved_at, s.team_challenge_id) AS place
        FROM team_challenge_solves s
        JOIN team_challenges tc ON tc.id = s.team_challenge_id
        JOIN event_teams t ON t.id = tc.event_team_id
        WHERE tc.event_id = sqlc.arg(event_id)
          AND NOT t.moderators
          AND event_team_visible(t.hidden, t.moderators, t.event_id, t.individual, t.admitted_manually, t.admission_locked, t.member_count)
    ) ranked
    JOIN event_teams team ON team.id = ranked.event_team_id
    LEFT JOIN event_challenges challenge ON challenge.id = ranked.event_challenge_id
    WHERE ranked.place = 1
), stand_failures AS (
    SELECT 'stand_failed'::text AS kind, x.at::timestamptz AS occurred_at, x.team_id::uuid AS team_id, team.name::text AS team_name,
           COALESCE(x.challenge_id, '00000000-0000-0000-0000-000000000000'::uuid)::uuid AS challenge_id, COALESCE(challenge.snapshot->>'name', '')::text AS challenge_name,
           COALESCE(x.reason, '')::text AS detail
    FROM event_stand_transitions x
    JOIN event_teams team ON team.id = x.team_id
    LEFT JOIN event_challenges challenge ON challenge.id = x.challenge_id
    WHERE x.event_id = sqlc.arg(event_id)
      AND NOT team.moderators
      AND x.source = 'stand'
      AND x.to_status = 3
), new_teams AS (
    SELECT 'team_created'::text AS kind, t.created_at::timestamptz AS occurred_at, t.id::uuid AS team_id, t.name::text AS team_name,
           '00000000-0000-0000-0000-000000000000'::uuid AS challenge_id, ''::text AS challenge_name, ''::text AS detail
    FROM event_teams t
    WHERE t.event_id = sqlc.arg(event_id)
      AND NOT t.moderators
      AND NOT t.individual
)
SELECT f.kind, f.occurred_at, f.team_id, f.team_name, f.challenge_id, f.challenge_name, f.detail
FROM (SELECT * FROM first_bloods UNION ALL SELECT * FROM stand_failures UNION ALL SELECT * FROM new_teams) f(kind, occurred_at, team_id, team_name, challenge_id, challenge_name, detail)
ORDER BY f.occurred_at DESC, f.team_id
LIMIT sqlc.arg(row_limit);
