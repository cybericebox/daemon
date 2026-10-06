-- Platform analytics, «Огляд» (docs/EVENT-ANALYTICS.md §8). Aggregates across
-- the whole platform. Every window is a half-open [from, to) of UTC days; the
-- previous window is passed as prev_from/prev_to (an empty window for "all
-- time"). The moderators team is never counted.

-- name: GetPlatformOverviewUsers :one
-- Accounts: total now, registered in the window and in the previous one.
SELECT count(*)::bigint                                                                          AS total,
       (count(*) FILTER (WHERE created_at >= sqlc.arg(from_at)::timestamptz
                             AND created_at < sqlc.arg(to_at)::timestamptz))::bigint            AS new_users,
       (count(*) FILTER (WHERE created_at >= sqlc.arg(prev_from)::timestamptz
                             AND created_at < sqlc.arg(prev_to)::timestamptz))::bigint         AS new_prev
FROM users
WHERE deleted_at IS NULL;

-- name: GetPlatformActiveUsers :one
-- Distinct accounts with recorded activity in the window and in the previous
-- one. Activity is a sign-in (session created) or a moment the account was
-- seen (session or account last_seen); expired sessions are purged, so older
-- windows are thinner than recent ones.
WITH activity AS (
    SELECT s.user_id, s.created_at AS at FROM sessions s
    UNION ALL
    SELECT s.user_id, s.last_seen FROM sessions s
    UNION ALL
    SELECT u.id, u.last_seen FROM users u WHERE u.last_seen IS NOT NULL
)
SELECT (count(DISTINCT a.user_id) FILTER (WHERE a.at >= sqlc.arg(from_at)::timestamptz
                                            AND a.at < sqlc.arg(to_at)::timestamptz))::bigint    AS active,
       (count(DISTINCT a.user_id) FILTER (WHERE a.at >= sqlc.arg(prev_from)::timestamptz
                                            AND a.at < sqlc.arg(prev_to)::timestamptz))::bigint AS active_prev
FROM activity a
JOIN users u ON u.id = a.user_id AND u.deleted_at IS NULL
WHERE a.at >= LEAST(sqlc.arg(from_at)::timestamptz, sqlc.arg(prev_from)::timestamptz)
  AND a.at < GREATEST(sqlc.arg(to_at)::timestamptz, sqlc.arg(prev_to)::timestamptz);

-- name: GetPlatformOverviewEvents :one
-- Events by lifecycle status at now (the same derivation as ListEventsPage):
-- draft = not_available + not_published, running = started, finished also
-- holds withdrawn. new_events were created in the window.
SELECT (count(*) FILTER (WHERE st = 'not_available' OR st = 'not_published'))::bigint AS draft,
       (count(*) FILTER (WHERE st = 'published'))::bigint                             AS published,
       (count(*) FILTER (WHERE st = 'started'))::bigint                               AS running,
       (count(*) FILTER (WHERE st = 'finished' OR st = 'withdrawn'))::bigint          AS finished,
       (count(*) FILTER (WHERE st = 'archived'))::bigint                              AS archived,
       count(*)::bigint                                                               AS total,
       (count(*) FILTER (WHERE created_at >= sqlc.arg(from_at)::timestamptz
                             AND created_at < sqlc.arg(to_at)::timestamptz))::bigint  AS new_events,
       (count(*) FILTER (WHERE created_at >= sqlc.arg(prev_from)::timestamptz
                             AND created_at < sqlc.arg(prev_to)::timestamptz))::bigint AS new_prev
FROM (SELECT created_at,
             CASE WHEN archive_at IS NOT NULL AND archive_at <= sqlc.arg(now)::timestamptz THEN 'archived'
                  WHEN available_from > sqlc.arg(now)::timestamptz THEN 'not_available'
                  WHEN NOT lifecycle_configured OR publish_at > sqlc.arg(now)::timestamptz THEN 'not_published'
                  WHEN withdraw_at IS NOT NULL AND withdraw_at <= sqlc.arg(now)::timestamptz THEN 'withdrawn'
                  WHEN start_at > sqlc.arg(now)::timestamptz THEN 'published'
                  WHEN (manual_finished_at IS NOT NULL AND manual_finished_at <= sqlc.arg(now)::timestamptz)
                    OR (finish_at IS NOT NULL AND finish_at <= sqlc.arg(now)::timestamptz) THEN 'finished'
                  ELSE 'started' END AS st
      FROM events) e;

-- name: GetPlatformOverviewParticipants :one
-- Participants across events: registered (an invitation not accepted yet is
-- not a registration) by creation time, approved by the decision time (the
-- registration time when it was approved on the spot).
SELECT (count(*) FILTER (WHERE NOT (p.invited AND p.status = 1)
                           AND p.created_at >= sqlc.arg(from_at)::timestamptz
                           AND p.created_at < sqlc.arg(to_at)::timestamptz))::bigint        AS registered,
       (count(*) FILTER (WHERE NOT (p.invited AND p.status = 1)
                           AND p.created_at >= sqlc.arg(prev_from)::timestamptz
                           AND p.created_at < sqlc.arg(prev_to)::timestamptz))::bigint     AS registered_prev,
       (count(*) FILTER (WHERE p.status = 2
                           AND COALESCE(p.decided_at, p.created_at) >= sqlc.arg(from_at)::timestamptz
                           AND COALESCE(p.decided_at, p.created_at) < sqlc.arg(to_at)::timestamptz))::bigint    AS approved,
       (count(*) FILTER (WHERE p.status = 2
                           AND COALESCE(p.decided_at, p.created_at) >= sqlc.arg(prev_from)::timestamptz
                           AND COALESCE(p.decided_at, p.created_at) < sqlc.arg(prev_to)::timestamptz))::bigint AS approved_prev
FROM event_participants p
LEFT JOIN event_teams t ON t.id = p.team_id
WHERE NOT COALESCE(t.moderators, false)
  AND (p.created_at >= LEAST(sqlc.arg(from_at)::timestamptz, sqlc.arg(prev_from)::timestamptz)
       OR COALESCE(p.decided_at, p.created_at) >= LEAST(sqlc.arg(from_at)::timestamptz, sqlc.arg(prev_from)::timestamptz));

-- name: GetPlatformOverviewActivity :one
-- Attempts and effective solves from the 5-minute buckets (the moderators
-- team is never in them). The join on events makes each event read its own
-- (event_id, bucket_at) primary key range.
SELECT (COALESCE(sum(b.attempts) FILTER (WHERE b.bucket_at >= sqlc.arg(from_at)::timestamptz
                                           AND b.bucket_at < sqlc.arg(to_at)::timestamptz), 0))::bigint    AS attempts,
       (COALESCE(sum(b.attempts) FILTER (WHERE b.bucket_at >= sqlc.arg(prev_from)::timestamptz
                                           AND b.bucket_at < sqlc.arg(prev_to)::timestamptz), 0))::bigint AS attempts_prev,
       (COALESCE(sum(b.solves) FILTER (WHERE b.bucket_at >= sqlc.arg(from_at)::timestamptz
                                         AND b.bucket_at < sqlc.arg(to_at)::timestamptz), 0))::bigint      AS solves,
       (COALESCE(sum(b.solves) FILTER (WHERE b.bucket_at >= sqlc.arg(prev_from)::timestamptz
                                         AND b.bucket_at < sqlc.arg(prev_to)::timestamptz), 0))::bigint   AS solves_prev
FROM events e
JOIN event_activity_buckets b ON b.event_id = e.id
     AND b.bucket_at >= LEAST(sqlc.arg(from_at)::timestamptz, sqlc.arg(prev_from)::timestamptz)
     AND b.bucket_at < GREATEST(sqlc.arg(to_at)::timestamptz, sqlc.arg(prev_to)::timestamptz);

-- name: GetPlatformOverviewMail :one
-- Email delivery targets handed to the transport (done) and failed (error)
-- per window. SMTP test sends are not counted.
SELECT (count(*) FILTER (WHERE tg.status = 'done' AND d.created_at >= sqlc.arg(from_at)::timestamptz
                           AND d.created_at < sqlc.arg(to_at)::timestamptz))::bigint        AS sent,
       (count(*) FILTER (WHERE tg.status = 'done' AND d.created_at >= sqlc.arg(prev_from)::timestamptz
                           AND d.created_at < sqlc.arg(prev_to)::timestamptz))::bigint     AS sent_prev,
       (count(*) FILTER (WHERE tg.status = 'error' AND d.created_at >= sqlc.arg(from_at)::timestamptz
                           AND d.created_at < sqlc.arg(to_at)::timestamptz))::bigint        AS failed,
       (count(*) FILTER (WHERE tg.status = 'error' AND d.created_at >= sqlc.arg(prev_from)::timestamptz
                           AND d.created_at < sqlc.arg(prev_to)::timestamptz))::bigint     AS failed_prev
FROM notification_dispatches d
JOIN notification_dispatch_targets tg ON tg.dispatch_id = d.id AND tg.channel = 'email'
WHERE d.notification_type <> 'smtp_test'
  AND d.created_at >= LEAST(sqlc.arg(from_at)::timestamptz, sqlc.arg(prev_from)::timestamptz)
  AND d.created_at < GREATEST(sqlc.arg(to_at)::timestamptz, sqlc.arg(prev_to)::timestamptz);

-- name: GetPlatformOverviewStands :one
-- Team stands right now (ready / creating / failed) and the failures logged
-- in each window (source stand, to_status failed = 3).
SELECT (SELECT count(*) FROM event_team_stands WHERE status = 2)::bigint AS ready,
       (SELECT count(*) FROM event_team_stands WHERE status = 1)::bigint AS creating,
       (SELECT count(*) FROM event_team_stands WHERE status = 3)::bigint AS failed_now,
       (SELECT count(*)
        FROM events e
        JOIN event_stand_transitions x ON x.event_id = e.id AND x.source = 'stand' AND x.to_status = 3
        WHERE x.at >= sqlc.arg(from_at)::timestamptz AND x.at < sqlc.arg(to_at)::timestamptz)::bigint AS failures,
       (SELECT count(*)
        FROM events e
        JOIN event_stand_transitions x ON x.event_id = e.id AND x.source = 'stand' AND x.to_status = 3
        WHERE x.at >= sqlc.arg(prev_from)::timestamptz AND x.at < sqlc.arg(prev_to)::timestamptz)::bigint AS failures_prev;

-- name: ListPlatformOverviewDailyUsers :many
-- New accounts per UTC day, zero-filled over [from, to).
SELECT (d.day AT TIME ZONE 'UTC')::timestamptz AS day,
       COALESCE(c.n, 0)::bigint                AS new_users
FROM generate_series(sqlc.arg(from_at)::timestamptz AT TIME ZONE 'UTC',
                     sqlc.arg(to_at)::timestamptz AT TIME ZONE 'UTC' - interval '1 day', interval '1 day') AS d(day)
LEFT JOIN (SELECT date_trunc('day', created_at AT TIME ZONE 'UTC') AS day, count(*) AS n
           FROM users
           WHERE deleted_at IS NULL
             AND created_at >= sqlc.arg(from_at)::timestamptz
             AND created_at < sqlc.arg(to_at)::timestamptz
           GROUP BY 1) c ON c.day = d.day
ORDER BY d.day;

-- name: ListPlatformOverviewDailyActivity :many
-- Attempts and solves per UTC day from the buckets, zero-filled over [from, to).
SELECT (d.day AT TIME ZONE 'UTC')::timestamptz AS day,
       COALESCE(c.attempts, 0)::bigint         AS attempts,
       COALESCE(c.solves, 0)::bigint           AS solves
FROM generate_series(sqlc.arg(from_at)::timestamptz AT TIME ZONE 'UTC',
                     sqlc.arg(to_at)::timestamptz AT TIME ZONE 'UTC' - interval '1 day', interval '1 day') AS d(day)
LEFT JOIN (SELECT date_trunc('day', b.bucket_at AT TIME ZONE 'UTC') AS day,
                  sum(b.attempts) AS attempts, sum(b.solves) AS solves
           FROM events e
           JOIN event_activity_buckets b ON b.event_id = e.id
                AND b.bucket_at >= sqlc.arg(from_at)::timestamptz
                AND b.bucket_at < sqlc.arg(to_at)::timestamptz
           GROUP BY 1) c ON c.day = d.day
ORDER BY d.day;

-- name: ListPlatformOverviewDailyMail :many
-- Email targets sent and failed per UTC day, zero-filled over [from, to).
SELECT (d.day AT TIME ZONE 'UTC')::timestamptz AS day,
       COALESCE(c.sent, 0)::bigint             AS sent,
       COALESCE(c.failed, 0)::bigint           AS failed
FROM generate_series(sqlc.arg(from_at)::timestamptz AT TIME ZONE 'UTC',
                     sqlc.arg(to_at)::timestamptz AT TIME ZONE 'UTC' - interval '1 day', interval '1 day') AS d(day)
LEFT JOIN (SELECT date_trunc('day', dd.created_at AT TIME ZONE 'UTC') AS day,
                  count(*) FILTER (WHERE tg.status = 'done')  AS sent,
                  count(*) FILTER (WHERE tg.status = 'error') AS failed
           FROM notification_dispatches dd
           JOIN notification_dispatch_targets tg ON tg.dispatch_id = dd.id AND tg.channel = 'email'
           WHERE dd.notification_type <> 'smtp_test'
             AND dd.created_at >= sqlc.arg(from_at)::timestamptz
             AND dd.created_at < sqlc.arg(to_at)::timestamptz
           GROUP BY 1) c ON c.day = d.day
ORDER BY d.day;
