-- name: ApplyLabTrafficTouch :exec
-- Overwrites the final row of a user x lab x access type with the collector's
-- cumulative values. Totals never go down (a stale resend cannot lower them),
-- first is min, last is max, first_responded_at the earliest answer.
INSERT INTO event_lab_touches (id, event_id, team_id, user_id, event_challenge_id, surface,
                               attempts_count, first_seen_at, last_seen_at, first_responded_at,
                               packets_out, packets_in, bytes_out, bytes_in)
VALUES (sqlc.arg(id), sqlc.arg(event_id), sqlc.arg(team_id), sqlc.arg(user_id)::uuid, sqlc.arg(event_challenge_id), sqlc.arg(surface),
        sqlc.arg(attempts_count), sqlc.arg(first_seen_at), sqlc.arg(last_seen_at), sqlc.narg(first_responded_at)::timestamptz,
        sqlc.arg(packets_out), sqlc.arg(packets_in), sqlc.arg(bytes_out), sqlc.arg(bytes_in))
ON CONFLICT (event_id, team_id, user_id, event_challenge_id, surface) WHERE user_id IS NOT NULL
DO UPDATE SET
    attempts_count     = GREATEST(event_lab_touches.attempts_count, EXCLUDED.attempts_count),
    first_seen_at      = LEAST(event_lab_touches.first_seen_at, EXCLUDED.first_seen_at),
    last_seen_at       = GREATEST(event_lab_touches.last_seen_at, EXCLUDED.last_seen_at),
    first_responded_at = LEAST(event_lab_touches.first_responded_at, EXCLUDED.first_responded_at),
    packets_out        = GREATEST(event_lab_touches.packets_out, EXCLUDED.packets_out),
    packets_in         = GREATEST(event_lab_touches.packets_in, EXCLUDED.packets_in),
    bytes_out          = GREATEST(event_lab_touches.bytes_out, EXCLUDED.bytes_out),
    bytes_in           = GREATEST(event_lab_touches.bytes_in, EXCLUDED.bytes_in);

-- name: ExtendLabTrafficCoverage :execrows
-- Grows the latest segment of the same collector boot when the new span joins
-- it (within the tolerance); zero rows means a new segment is needed.
UPDATE lab_traffic_coverage
SET covered_from = LEAST(covered_from, sqlc.arg(covered_from)::timestamptz),
    covered_to   = GREATEST(covered_to, sqlc.arg(covered_to)::timestamptz)
WHERE id = (SELECT s.id
            FROM lab_traffic_coverage s
            WHERE s.event_id = sqlc.arg(event_id)
              AND s.team_id = sqlc.arg(team_id)
              AND s.surface = sqlc.arg(surface)
              AND s.source = sqlc.arg(source)
              AND s.boot_id = sqlc.arg(boot_id)
              AND s.partial = sqlc.arg(partial)
              AND s.covered_to >= sqlc.arg(covered_from)::timestamptz - make_interval(secs => sqlc.arg(tolerance_seconds)::double precision)
            ORDER BY s.covered_to DESC
            LIMIT 1);

-- name: CreateLabTrafficCoverage :exec
-- A no-op for an event that no longer exists (a stale group).
INSERT INTO lab_traffic_coverage (id, event_id, team_id, surface, source, boot_id, covered_from, covered_to, partial)
SELECT sqlc.arg(id), e.id, sqlc.arg(team_id), sqlc.arg(surface), sqlc.arg(source), sqlc.arg(boot_id),
       sqlc.arg(covered_from)::timestamptz, sqlc.arg(covered_to)::timestamptz, sqlc.arg(partial)::boolean
FROM events e
WHERE e.id = sqlc.arg(event_id);

-- name: ListLabTrafficCoverage :many
-- Spans of a team that overlap [since, until].
SELECT surface, covered_from, covered_to, partial
FROM lab_traffic_coverage
WHERE event_id = sqlc.arg(event_id)
  AND team_id = sqlc.arg(team_id)
  AND covered_to >= sqlc.arg(since)::timestamptz
  AND covered_from <= sqlc.arg(until)::timestamptz
ORDER BY surface, covered_from;

-- name: SummarizeLabTouches :many
-- What a user (or, with a nil user, anybody in the team) did against one task
-- before a moment. One row per surface.
SELECT surface,
       COALESCE(SUM(attempts_count), 0)::bigint AS attempts,
       MIN(first_seen_at)::timestamptz          AS first_seen_at,
       -- 0001-01-01 stands for «never answered».
       COALESCE(MIN(first_responded_at), '0001-01-01 00:00:00+00'::timestamptz)::timestamptz AS first_responded_at,
       COALESCE(SUM(bytes_in), 0)::bigint       AS bytes_in
FROM event_lab_touches
WHERE event_id = sqlc.arg(event_id)
  AND team_id = sqlc.arg(team_id)
  AND event_challenge_id = sqlc.arg(event_challenge_id)
  AND (sqlc.arg(user_id)::uuid = '00000000-0000-0000-0000-000000000000' OR user_id = sqlc.arg(user_id)::uuid)
  AND first_seen_at < sqlc.arg(before)::timestamptz
GROUP BY surface;

-- name: GetLabDeployedSince :one
-- The earliest moment a lab of this task existed for the team; 0001-01-01 when
-- it was never deployed.
SELECT COALESCE(MIN(deployed_at), '0001-01-01 00:00:00+00'::timestamptz)::timestamptz AS deployed_at
FROM lab_bindings
WHERE event_id = sqlc.arg(event_id)
  AND event_team_id = sqlc.arg(event_team_id)
  AND event_challenge_id = sqlc.arg(event_challenge_id);

-- name: IsUserInEventTeam :one
SELECT EXISTS (SELECT 1
               FROM event_participants
               WHERE event_id = sqlc.arg(event_id)
                 AND team_id = sqlc.arg(team_id)
                 AND user_id = sqlc.arg(user_id)) AS member;

-- name: PurgeEventLabTouches :execrows
-- Aggregates of events that ended before the cutoff.
DELETE FROM event_lab_touches
WHERE id IN (SELECT t.id
             FROM event_lab_touches t
             JOIN events e ON e.id = t.event_id
             WHERE e.lifecycle_configured
               AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(ended_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF t SKIP LOCKED);

-- name: PurgeEventLabTrafficCoverage :execrows
DELETE FROM lab_traffic_coverage
WHERE id IN (SELECT c.id
             FROM lab_traffic_coverage c
             JOIN events e ON e.id = c.event_id
             WHERE e.lifecycle_configured
               AND LEAST(e.manual_finished_at, e.finish_at) < sqlc.arg(ended_before)::timestamptz
             LIMIT sqlc.arg(batch_size)
             FOR UPDATE OF c SKIP LOCKED);
