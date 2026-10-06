-- Platform analytics, «Сповіщення» (docs/EVENT-ANALYTICS.md §8): delivery health
-- of the notification channels. One row of notification_dispatch_targets is one
-- delivery: status 'done' is sent, 'error' is failed, 'deferred' is held back by
-- the SMTP send limit. The channel filter ('' = all, 'email', 'in_app') narrows
-- the counts; the transport (event / platform / env) exists for email targets
-- only, so the transport grouping, the transport filter and the failure reasons
-- are email-scoped. Older email rows have no transport and read as 'unknown'. SMTP test sends are journaled as dispatches of kind
-- smtp_test and are left out unless include_tests is set. No recipient
-- address is ever selected.

-- name: ListPlatformMailSummary :many
-- Sent / failed / deferred / fallback counts per UTC day, per transport, per
-- channel and per notification type in ONE pass (grouping sets). dimension says
-- which grouping a row belongs to; day is set for 'day' rows, key for the
-- 'transport', 'channel' and 'type' rows. fallbacks counts sends that first failed on the Event SMTP and went out
-- through the platform transport.
WITH tg AS (
    SELECT date_trunc('day', d.created_at AT TIME ZONE 'UTC')       AS day,
           CASE WHEN t.channel = 'email' THEN COALESCE(NULLIF(t.transport, ''), 'unknown') END AS transport,
           t.channel                                               AS channel,
           d.notification_type                                     AS ntype,
           t.status                                                AS status,
           (t.fallback_error <> '')                                AS fallback
    FROM notification_dispatch_targets t
    JOIN notification_dispatches d ON d.id = t.dispatch_id
    WHERE d.created_at >= sqlc.arg(from_at)::timestamptz
      AND d.created_at < sqlc.arg(to_at)::timestamptz
      AND (sqlc.arg(channel)::text = '' OR t.channel = sqlc.arg(channel)::text)
      AND (sqlc.arg(include_tests)::boolean OR d.notification_type <> 'smtp_test')
      AND (sqlc.arg(transport)::text = '' OR (t.channel = 'email' AND COALESCE(NULLIF(t.transport, ''), 'unknown') = sqlc.arg(transport)::text))
      AND (sqlc.arg(notification_type)::text = '' OR d.notification_type = sqlc.arg(notification_type)::text)
)
SELECT (CASE WHEN GROUPING(day) = 0 THEN 'day' WHEN GROUPING(transport) = 0 THEN 'transport' WHEN GROUPING(channel) = 0 THEN 'channel' ELSE 'type' END)::text AS dimension,
       COALESCE(day AT TIME ZONE 'UTC', 'epoch'::timestamptz)::timestamptz AS day,
       COALESCE(transport, channel, ntype, '')::text                       AS key,
       count(*) FILTER (WHERE status = 'done')::bigint                     AS sent,
       count(*) FILTER (WHERE status = 'error')::bigint                    AS failed,
       count(*) FILTER (WHERE status = 'deferred')::bigint                 AS deferred,
       count(*) FILTER (WHERE fallback)::bigint                            AS fallbacks
FROM tg
GROUP BY GROUPING SETS ((day), (transport), (channel), (ntype))
-- non-email targets have no transport: they form no transport row
HAVING GROUPING(transport) = 1 OR transport IS NOT NULL;

-- name: ListPlatformMailErrors :many
-- Top failure reasons. The stored SMTP error is normalized inside the query so
-- equal problems group together and nothing personal leaves the database:
-- e-mail addresses, IP addresses, long ids and ports are replaced by
-- placeholders and the text is cut. code is the enhanced SMTP status (for
-- example "550 5.1.1"), empty when the text has none.
WITH e AS (
    SELECT d.created_at,
           left(btrim(regexp_replace(
               regexp_replace(
                   regexp_replace(
                       regexp_replace(
                           regexp_replace(t.error, '[[:alnum:]._%+''-]+@[[:alnum:].-]+', '<address>', 'g'),
                           '\[?[0-9a-fA-F:]*:[0-9a-fA-F:]*:[0-9a-fA-F:.]*\]?|\m[0-9]{1,3}(\.[0-9]{1,3}){3}\M', '<ip>', 'g'),
                       '\m[0-9a-fA-F]{8,}(-[0-9a-fA-F]{4,})*\M', '<id>', 'g'),
                   ':[0-9]{2,5}\M', ':<port>', 'g'),
               '\s+', ' ', 'g')), 160) AS message,
           COALESCE((regexp_match(t.error, '\m([245][0-9]{2})[ -]([245]\.[0-9]{1,3}\.[0-9]{1,3})\M'))[1] || ' ' ||
                    (regexp_match(t.error, '\m([245][0-9]{2})[ -]([245]\.[0-9]{1,3}\.[0-9]{1,3})\M'))[2], '') AS code
    FROM notification_dispatch_targets t
    JOIN notification_dispatches d ON d.id = t.dispatch_id
    WHERE t.channel = 'email'
      AND t.status = 'error'
      AND d.created_at >= sqlc.arg(from_at)::timestamptz
      AND d.created_at < sqlc.arg(to_at)::timestamptz
      AND (sqlc.arg(include_tests)::boolean OR d.notification_type <> 'smtp_test')
      AND (sqlc.arg(transport)::text = '' OR COALESCE(NULLIF(t.transport, ''), 'unknown') = sqlc.arg(transport)::text)
      AND (sqlc.arg(notification_type)::text = '' OR d.notification_type = sqlc.arg(notification_type)::text)
)
SELECT e.code::text                 AS code,
       e.message::text              AS message,
       count(*)::bigint             AS total,
       max(e.created_at)::timestamptz AS last_at
FROM e
GROUP BY e.code, e.message
ORDER BY count(*) DESC, e.message, e.code
LIMIT sqlc.arg(limit_val);

-- name: ListPlatformMailOptions :many
-- The values the transport and type filters offer: every email transport and
-- every type seen in the period on the chosen channel (test sends only with
-- include_tests), whatever the transport and type filters are.
WITH tg AS (
    SELECT CASE WHEN t.channel = 'email' THEN COALESCE(NULLIF(t.transport, ''), 'unknown') END AS transport,
           d.notification_type                          AS ntype
    FROM notification_dispatch_targets t
    JOIN notification_dispatches d ON d.id = t.dispatch_id
    WHERE d.created_at >= sqlc.arg(from_at)::timestamptz
      AND d.created_at < sqlc.arg(to_at)::timestamptz
      AND (sqlc.arg(channel)::text = '' OR t.channel = sqlc.arg(channel)::text)
      AND (sqlc.arg(include_tests)::boolean OR d.notification_type <> 'smtp_test')
)
SELECT 'transport'::text AS kind, transport::text AS value FROM tg WHERE transport IS NOT NULL GROUP BY transport
UNION ALL
SELECT 'type'::text, ntype::text FROM tg GROUP BY ntype
ORDER BY 1, 2;
