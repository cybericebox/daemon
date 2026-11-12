-- The platform admin dashboard (docs/specs/ADMIN.md §3): every block is a handful of statements, never one per row.

-- name: DashboardFailedStands :many
-- Events with failed stands (events not finished or archived), the worst first, with the totals over all of them.
WITH failed AS (
    SELECT s.event_id, count(*)::bigint AS stands
    FROM event_team_stands s
    JOIN events e ON e.id = s.event_id
    WHERE s.status = 3
      AND (e.archive_at IS NULL OR e.archive_at > sqlc.arg(now)::timestamptz)
      AND (e.manual_finished_at IS NULL OR e.manual_finished_at > sqlc.arg(now)::timestamptz)
      AND (e.finish_at IS NULL OR e.finish_at > sqlc.arg(now)::timestamptz)
    GROUP BY s.event_id
)
SELECT f.event_id, e.name::text AS event_name, f.stands,
       (SELECT sum(stands) FROM failed)::bigint AS total_stands,
       (SELECT count(*) FROM failed)::bigint    AS total_events
FROM failed f
JOIN events e ON e.id = f.event_id
ORDER BY f.stands DESC, e.name
LIMIT sqlc.arg(row_limit);

-- name: DashboardEventsWithoutReservation :many
-- Events that run stands, have not finished and start within the horizon (or run now), with no active reservation.
WITH due AS (
    SELECT e.id, e.name, e.start_at
    FROM events e
    WHERE e.infrastructure_allowed
      AND e.lifecycle_configured
      AND (e.archive_at IS NULL OR e.archive_at > sqlc.arg(now)::timestamptz)
      AND (e.manual_finished_at IS NULL OR e.manual_finished_at > sqlc.arg(now)::timestamptz)
      AND (e.finish_at IS NULL OR e.finish_at > sqlc.arg(now)::timestamptz)
      AND e.start_at < sqlc.arg(horizon)::timestamptz
      AND NOT EXISTS (SELECT 1 FROM resource_reservations r WHERE r.event_id = e.id AND r.kind = 'event' AND r.canceled_at IS NULL)
)
SELECT d.id AS event_id, d.name::text AS event_name, d.start_at, (SELECT count(*) FROM due)::bigint AS total_events
FROM due d
ORDER BY d.start_at, d.id
LIMIT sqlc.arg(row_limit);

-- name: DashboardPendingElevations :one
-- oldest_at is 'epoch' without a pending request.
SELECT count(*)::bigint AS total, COALESCE(min(requested_at), 'epoch'::timestamptz)::timestamptz AS oldest_at
FROM exercise_resource_elevations
WHERE status = 0;

-- name: DashboardOpenErrors :many
SELECT g.id, g.title, g.occurrences, g.last_seen_at, (SELECT count(*) FROM error_groups WHERE status = 'open')::bigint AS total_groups
FROM error_groups g
WHERE g.status = 'open'
ORDER BY g.last_seen_at DESC, g.id
LIMIT sqlc.arg(row_limit);

-- name: DashboardMailErrors :one
-- Deliveries that ended in an error since the given moment.
SELECT count(*)::bigint AS total
FROM notification_dispatch_targets t
WHERE t.status = 'error'
  AND t.updated_at >= sqlc.arg(since)::timestamptz;

-- name: DashboardCurrentEvents :many
-- Events that run now or start before the horizon, with their counters.
SELECT e.id, e.name::text AS name, e.tag, e.start_at, e.finish_at,
       (e.start_at <= sqlc.arg(now)::timestamptz)::boolean AS running,
       (SELECT count(*) FROM event_participants p WHERE p.event_id = e.id AND p.status = 2)::bigint AS participants,
       (SELECT count(*) FROM event_teams t WHERE t.event_id = e.id AND NOT t.moderators AND NOT t.individual)::bigint AS teams,
       (SELECT count(*) FROM event_team_stands s JOIN event_teams t ON t.id = s.event_team_id
        WHERE s.event_id = e.id AND NOT t.moderators AND s.status = 2)::bigint AS stands_ready,
       (SELECT count(*) FROM event_team_stands s JOIN event_teams t ON t.id = s.event_team_id
        WHERE s.event_id = e.id AND NOT t.moderators)::bigint AS stands_total
FROM events e
WHERE e.lifecycle_configured
  AND (e.archive_at IS NULL OR e.archive_at > sqlc.arg(now)::timestamptz)
  AND (e.manual_finished_at IS NULL OR e.manual_finished_at > sqlc.arg(now)::timestamptz)
  AND (e.finish_at IS NULL OR e.finish_at > sqlc.arg(now)::timestamptz)
  AND e.start_at < sqlc.arg(horizon)::timestamptz
ORDER BY e.start_at, e.id
LIMIT sqlc.arg(row_limit);

-- name: DashboardTrends :many
-- Three daily series for the days from the given day on (UTC days): sign-ups, users with a session, solved tasks.
SELECT d.day::timestamptz AS day,
       (SELECT count(*) FROM users u WHERE u.deleted_at IS NULL AND u.created_at >= d.day AND u.created_at < d.day + interval '1 day')::bigint AS registrations,
       (SELECT count(DISTINCT s.user_id) FROM sessions s WHERE s.created_at >= d.day AND s.created_at < d.day + interval '1 day')::bigint AS active_users,
       (SELECT count(*) FROM team_challenge_solves v WHERE v.solved_at >= d.day AND v.solved_at < d.day + interval '1 day')::bigint AS solves
FROM generate_series(date_trunc('day', sqlc.arg(from_day)::timestamptz), date_trunc('day', sqlc.arg(from_day)::timestamptz) + (sqlc.arg(days)::int - 1) * interval '1 day', interval '1 day') AS d(day)
ORDER BY d.day;
