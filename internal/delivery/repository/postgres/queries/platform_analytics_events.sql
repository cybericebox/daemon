-- Platform analytics «Заходи» (docs/EVENT-ANALYTICS.md §8). Aggregates only,
-- across events; the moderators team is never counted. Period bounds are
-- [from_at, to_at), aligned to UTC days. Lifecycle status codes match
-- eventModel.LifecycleStatus: 0 not published, 1 published, 2 started,
-- 3 finished, 4 withdrawn.

-- name: ListPlatformAnalyticsEventSeries :many
-- Per UTC day of the period: events created, events that start (a configured
-- schedule) and registrations. A registration is a participant row that is
-- not a pending invitation (as in the event overview).
WITH days AS (
    SELECT d::date AS day
    FROM generate_series((sqlc.arg(from_at)::timestamptz AT TIME ZONE 'UTC')::date,
                         (sqlc.arg(to_at)::timestamptz AT TIME ZONE 'UTC')::date - 1,
                         interval '1 day') d
), created AS (
    SELECT (e.created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n
    FROM events e
    WHERE e.created_at >= sqlc.arg(from_at)::timestamptz AND e.created_at < sqlc.arg(to_at)::timestamptz
    GROUP BY 1
), started AS (
    SELECT (e.start_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n
    FROM events e
    WHERE e.lifecycle_configured
      AND e.start_at >= sqlc.arg(from_at)::timestamptz AND e.start_at < sqlc.arg(to_at)::timestamptz
    GROUP BY 1
), registered AS (
    SELECT (p.created_at AT TIME ZONE 'UTC')::date AS day, count(*) AS n
    FROM event_participants p
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE p.created_at >= sqlc.arg(from_at)::timestamptz AND p.created_at < sqlc.arg(to_at)::timestamptz
      AND NOT (p.invited AND p.status = 1)
      AND NOT COALESCE(t.moderators, false)
    GROUP BY 1
)
SELECT days.day::date                        AS day,
       COALESCE(created.n, 0)::bigint        AS events_created,
       COALESCE(started.n, 0)::bigint        AS events_started,
       COALESCE(registered.n, 0)::bigint     AS registrations
FROM days
LEFT JOIN created ON created.day = days.day
LEFT JOIN started ON started.day = days.day
LEFT JOIN registered ON registered.day = days.day
ORDER BY days.day;

-- name: ListPlatformAnalyticsEventStatuses :many
-- Events of the period grouped by lifecycle status at as_of. An event is in
-- the period when its schedule overlaps it (configured) or it was created in
-- it (a draft that was never scheduled).
WITH scoped AS (
    SELECT e.id,
           (CASE
                WHEN NOT e.lifecycle_configured THEN 0
                WHEN e.withdraw_at IS NOT NULL AND e.withdraw_at <= sqlc.arg(as_of)::timestamptz THEN 4
                WHEN sqlc.arg(as_of)::timestamptz < e.publish_at THEN 0
                WHEN sqlc.arg(as_of)::timestamptz < e.start_at THEN 1
                WHEN (e.manual_finished_at IS NOT NULL AND e.manual_finished_at <= sqlc.arg(as_of)::timestamptz)
                     OR (e.finish_at IS NOT NULL AND e.finish_at <= sqlc.arg(as_of)::timestamptz) THEN 3
                ELSE 2
            END)::smallint AS status
    FROM events e
    WHERE (e.lifecycle_configured
           AND e.start_at < sqlc.arg(to_at)::timestamptz
           AND COALESCE(LEAST(e.finish_at, e.manual_finished_at), 'infinity'::timestamptz) > sqlc.arg(from_at)::timestamptz)
       OR (NOT e.lifecycle_configured
           AND e.created_at >= sqlc.arg(from_at)::timestamptz AND e.created_at < sqlc.arg(to_at)::timestamptz)
)
SELECT status, count(*)::bigint AS events
FROM scoped
GROUP BY status
ORDER BY status;

-- name: ListPlatformAnalyticsEvents :many
-- The events of the period (same scope as the status counts), newest start
-- first, at most row_limit. total is the size of the whole scope. The figures
-- are aggregates per event: registered participants, teams (the moderators
-- team excluded), solves and the teams with at least one solve. The
-- effective finish is the earlier of the scheduled and the manual finish
-- (the epoch when there is none).
WITH scoped AS (
    SELECT e.id, e.tag, e.name, e.start_at, e.lifecycle_configured,
           e.publish_at, e.finish_at, e.withdraw_at, e.manual_finished_at, e.created_at,
           count(*) OVER () AS total
    FROM events e
    WHERE (e.lifecycle_configured
           AND e.start_at < sqlc.arg(to_at)::timestamptz
           AND COALESCE(LEAST(e.finish_at, e.manual_finished_at), 'infinity'::timestamptz) > sqlc.arg(from_at)::timestamptz)
       OR (NOT e.lifecycle_configured
           AND e.created_at >= sqlc.arg(from_at)::timestamptz AND e.created_at < sqlc.arg(to_at)::timestamptz)
    ORDER BY e.start_at DESC, e.id DESC
    LIMIT sqlc.arg(row_limit)
), participants AS (
    SELECT p.event_id, count(*) AS n
    FROM event_participants p
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE p.event_id IN (SELECT id FROM scoped)
      AND NOT (p.invited AND p.status = 1)
      AND NOT COALESCE(t.moderators, false)
    GROUP BY p.event_id
), teams AS (
    SELECT t.event_id, count(*) AS n
    FROM event_teams t
    WHERE t.event_id IN (SELECT id FROM scoped) AND NOT t.moderators
    GROUP BY t.event_id
), solves AS (
    SELECT tc.event_id, count(*) AS n, count(DISTINCT tc.event_team_id) AS teams
    FROM team_challenge_solves s
    JOIN team_challenges tc ON tc.id = s.team_challenge_id
    JOIN event_teams t ON t.id = tc.event_team_id AND NOT t.moderators
    WHERE tc.event_id IN (SELECT id FROM scoped)
    GROUP BY tc.event_id
)
SELECT s.id                                                AS event_id,
       s.tag::text                                         AS tag,
       s.name::text                                        AS name,
       (CASE
            WHEN NOT s.lifecycle_configured THEN 0
            WHEN s.withdraw_at IS NOT NULL AND s.withdraw_at <= sqlc.arg(as_of)::timestamptz THEN 4
            WHEN sqlc.arg(as_of)::timestamptz < s.publish_at THEN 0
            WHEN sqlc.arg(as_of)::timestamptz < s.start_at THEN 1
            WHEN (s.manual_finished_at IS NOT NULL AND s.manual_finished_at <= sqlc.arg(as_of)::timestamptz)
                 OR (s.finish_at IS NOT NULL AND s.finish_at <= sqlc.arg(as_of)::timestamptz) THEN 3
            ELSE 2
        END)::smallint                                     AS status,
       s.lifecycle_configured::boolean                     AS configured,
       s.start_at::timestamptz                             AS start_at,
       COALESCE(LEAST(s.finish_at, s.manual_finished_at), 'epoch'::timestamptz)::timestamptz AS finish_at,
       COALESCE(pa.n, 0)::bigint                           AS participants,
       COALESCE(te.n, 0)::bigint                           AS teams,
       COALESCE(so.n, 0)::bigint                           AS solves,
       COALESCE(so.teams, 0)::bigint                       AS teams_solved,
       s.total::bigint                                     AS total
FROM scoped s
LEFT JOIN participants pa ON pa.event_id = s.id
LEFT JOIN teams te ON te.event_id = s.id
LEFT JOIN solves so ON so.event_id = s.id
ORDER BY s.start_at DESC, s.id DESC;

-- name: ListPlatformAnalyticsUpcomingEvents :many
-- The next events by start date (scheduled, not started and not withdrawn),
-- with the registrations so far. Not bound to the period.
WITH next AS (
    SELECT e.id, e.tag, e.name, e.start_at, e.publish_at
    FROM events e
    WHERE e.lifecycle_configured
      AND e.start_at > sqlc.arg(as_of)::timestamptz
      AND (e.withdraw_at IS NULL OR e.withdraw_at > sqlc.arg(as_of)::timestamptz)
    ORDER BY e.start_at, e.id
    LIMIT sqlc.arg(row_limit)
), registered AS (
    SELECT p.event_id, count(*) AS n
    FROM event_participants p
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE p.event_id IN (SELECT id FROM next)
      AND NOT (p.invited AND p.status = 1)
      AND NOT COALESCE(t.moderators, false)
    GROUP BY p.event_id
)
SELECT n.id                                  AS event_id,
       n.tag::text                           AS tag,
       n.name::text                          AS name,
       n.start_at::timestamptz               AS start_at,
       (n.publish_at <= sqlc.arg(as_of)::timestamptz)::boolean AS published,
       COALESCE(r.n, 0)::bigint              AS registrations
FROM next n
LEFT JOIN registered r ON r.event_id = n.id
ORDER BY n.start_at, n.id;
