-- Event analytics (docs/EVENT-ANALYTICS.md): the rollup job's statements and
-- the reads of the analytics API. The moderators team is never counted.

-- name: ListEventAnalyticsRollupCandidates :many
-- Events the rollup job still has work for: started, and without finalized
-- buckets (or with results changed since), or with a VPN rollup still open
-- (infrastructure events). Running events come first.
SELECT e.id,
       e.start_at,
       (LEAST(e.manual_finished_at, e.finish_at) IS NOT NULL)::boolean AS has_finish,
       COALESCE(LEAST(e.manual_finished_at, e.finish_at), e.start_at)::timestamptz AS finished_at,
       e.infrastructure_allowed,
       COALESCE(r.revision, 0)::bigint                       AS revision,
       COALESCE(s.buckets_revision, 0)::bigint                AS buckets_revision,
       s.buckets_finalized_at,
       s.vpn_finalized_at,
       s.vpn_cursor_received_at,
       s.vpn_cursor_id
FROM events e
LEFT JOIN event_result_revisions r ON r.event_id = e.id
LEFT JOIN event_analytics_rollups s ON s.event_id = e.id
WHERE e.lifecycle_configured
  AND e.start_at <= sqlc.arg(now)::timestamptz
  AND (s.event_id IS NULL
    OR s.buckets_finalized_at IS NULL
    OR s.buckets_revision <> COALESCE(r.revision, 0)
    OR (e.infrastructure_allowed AND s.vpn_finalized_at IS NULL))
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
       COALESCE((c -> 'status' -> 'statistics' ->> 'lastHandshakeUnix')::bigint, 0)::bigint AS handshake_unix,
       COALESCE((c -> 'status' -> 'statistics' ->> 'rxBytes')::bigint, 0)::bigint   AS rx_bytes,
       COALESCE((c -> 'status' -> 'statistics' ->> 'txBytes')::bigint, 0)::bigint   AS tx_bytes
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
