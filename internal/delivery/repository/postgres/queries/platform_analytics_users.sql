-- Platform analytics, «Користувачі» (docs/EVENT-ANALYTICS.md §8). Extends
-- the admin user stats (CountUsersByRoleAll, CountUsersByStatus, the new /
-- active counts of platform_analytics_overview.sql) with period-bound
-- aggregates. Windows are half-open [from, to) of UTC days. Only
-- ListPlatformUsersPeople returns per-user rows.

-- name: ListPlatformUsersDailyActive :many
-- Daily and weekly active accounts per UTC day, zero-filled over [from, to).
-- dau: accounts with recorded activity that day; wau: accounts with activity
-- in the 7 days ending that day. Activity is a sign-in (session created) or a
-- moment the account was seen (session or account last_seen); expired
-- sessions are purged, so older days are thinner than recent ones.
WITH activity AS (
    SELECT DISTINCT x.user_id, date_trunc('day', x.at AT TIME ZONE 'UTC') AS day
    FROM (SELECT s.user_id, s.created_at AS at FROM sessions s
          UNION ALL
          SELECT s.user_id, s.last_seen FROM sessions s
          UNION ALL
          SELECT u.id, u.last_seen FROM users u WHERE u.last_seen IS NOT NULL) x
    JOIN users u ON u.id = x.user_id AND u.deleted_at IS NULL
    WHERE x.at >= sqlc.arg(from_at)::timestamptz - interval '6 days'
      AND x.at < sqlc.arg(to_at)::timestamptz
)
SELECT (d.day AT TIME ZONE 'UTC')::timestamptz                       AS day,
       (count(a.user_id) FILTER (WHERE a.day = d.day))::bigint       AS dau,
       count(DISTINCT a.user_id)::bigint                             AS wau
FROM generate_series(sqlc.arg(from_at)::timestamptz AT TIME ZONE 'UTC',
                     sqlc.arg(to_at)::timestamptz AT TIME ZONE 'UTC' - interval '1 day', interval '1 day') AS d(day)
LEFT JOIN activity a ON a.day > d.day - interval '7 days' AND a.day <= d.day
GROUP BY d.day
ORDER BY d.day;

-- name: GetPlatformUsersMethods :one
-- Sign-in methods of the current accounts, exclusive groups: password only
-- (a password, no provider), Google only (a linked provider, no password),
-- both, and none (neither, e.g. an invited account that has not finished
-- registration). *_new count the accounts registered in the window.
WITH u AS (
    SELECT (COALESCE(hashed_password, '') <> '') AS pw,
           EXISTS (SELECT 1 FROM user_providers p WHERE p.user_id = users.id) AS prov,
           (created_at >= sqlc.arg(from_at)::timestamptz AND created_at < sqlc.arg(to_at)::timestamptz) AS is_new
    FROM users
    WHERE deleted_at IS NULL
)
SELECT (count(*) FILTER (WHERE pw AND NOT prov))::bigint                AS password_only,
       (count(*) FILTER (WHERE prov AND NOT pw))::bigint                AS provider_only,
       (count(*) FILTER (WHERE pw AND prov))::bigint                    AS both,
       (count(*) FILTER (WHERE NOT pw AND NOT prov))::bigint            AS none,
       (count(*) FILTER (WHERE is_new AND pw AND NOT prov))::bigint     AS password_only_new,
       (count(*) FILTER (WHERE is_new AND prov AND NOT pw))::bigint     AS provider_only_new,
       (count(*) FILTER (WHERE is_new AND pw AND prov))::bigint         AS both_new,
       (count(*) FILTER (WHERE is_new AND NOT pw AND NOT prov))::bigint AS none_new
FROM u;

-- name: GetPlatformUsersRetention :one
-- How many events each account took part in (approved, moderators team
-- excluded) up to the end of the window: 1, 2, 3 or more, and never.
WITH joined AS (
    SELECT p.user_id, count(*) AS n
    FROM event_participants p
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE p.status = 2
      AND NOT COALESCE(t.moderators, false)
      AND COALESCE(p.decided_at, p.created_at) < sqlc.arg(to_at)::timestamptz
    GROUP BY p.user_id
)
SELECT (count(*) FILTER (WHERE j.n = 1))::bigint  AS one_event,
       (count(*) FILTER (WHERE j.n = 2))::bigint  AS two_events,
       (count(*) FILTER (WHERE j.n >= 3))::bigint AS three_plus_events,
       (SELECT count(*) FROM users u
        WHERE u.deleted_at IS NULL AND u.created_at < sqlc.arg(to_at)::timestamptz
          AND NOT EXISTS (SELECT 1 FROM joined x WHERE x.user_id = u.id))::bigint AS never
FROM joined j
JOIN users u ON u.id = j.user_id AND u.deleted_at IS NULL;

-- name: ListPlatformUsersPeople :many
-- The most active accounts: events joined (approved, moderators team
-- excluded), then most recently seen; at most `limit_val` rows. Solves are
-- counted for these rows only (distinct team tasks with an effectively
-- correct attempt of the account). Per-user data: super_admin only.
WITH top AS (
    SELECT p.user_id, count(*)::bigint AS events_joined
    FROM event_participants p
    LEFT JOIN event_teams t ON t.id = p.team_id
    WHERE p.status = 2 AND NOT COALESCE(t.moderators, false)
    GROUP BY p.user_id
), ranked AS (
    SELECT u.id, u.first_name, u.last_name, u.email, u.role, top.events_joined, u.last_seen
    FROM top
    JOIN users u ON u.id = top.user_id AND u.deleted_at IS NULL
    ORDER BY top.events_joined DESC, u.last_seen DESC NULLS LAST, u.id
    LIMIT sqlc.arg(limit_val)
)
SELECT r.id, r.first_name, r.last_name, r.email, r.role, r.events_joined, r.last_seen,
       COALESCE((SELECT count(DISTINCT a.team_challenge_id)
                 FROM events e
                 JOIN effective_challenge_attempts a ON a.event_id = e.id AND a.user_id = r.id
                 JOIN event_teams t ON t.id = a.event_team_id AND NOT t.moderators
                 WHERE a.effective_correct), 0)::bigint AS solves
FROM ranked r
ORDER BY r.events_joined DESC, r.last_seen DESC NULLS LAST, r.id;
