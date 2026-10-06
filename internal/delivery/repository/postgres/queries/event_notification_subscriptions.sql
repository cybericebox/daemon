-- Event rows are overrides of platform_signal_notification_defaults: the
-- effective subscription for (event, signal, channel) is the Event row when it
-- exists, otherwise the platform default.

-- name: ListEffectiveEnabledSignalNotificationSubscriptions :many
SELECT COALESCE(e.channel, d.channel)::text AS channel,
       COALESCE(e.audience, d.audience)::jsonb AS audience
FROM (SELECT pd.* FROM platform_signal_notification_defaults pd
      WHERE pd.signal_type = sqlc.arg(signal_type)) d
FULL JOIN (SELECT es.* FROM event_signal_notification_subscriptions es
           WHERE es.scope_event_id = sqlc.arg(scope_event_id) AND es.signal_type = sqlc.arg(signal_type)) e
  ON e.channel = d.channel
WHERE COALESCE(e.enabled, d.enabled)
ORDER BY 1;

-- name: ListEffectiveEventSignalNotificationSubscriptions :many
-- config is the platform default with the Event keys laid over it.
SELECT COALESCE(e.signal_type, d.signal_type)::text AS signal_type,
       COALESCE(e.channel, d.channel)::text AS channel,
       COALESCE(e.enabled, d.enabled)::bool AS enabled,
       COALESCE(e.audience, d.audience)::jsonb AS audience,
       (COALESCE(d.config, '{}'::jsonb) || COALESCE(e.config, '{}'::jsonb))::jsonb AS config,
       (CASE WHEN e.scope_event_id IS NULL THEN 'platform' ELSE 'event' END)::text AS source
FROM platform_signal_notification_defaults d
FULL JOIN (SELECT * FROM event_signal_notification_subscriptions WHERE scope_event_id = $1) e
  ON e.signal_type = d.signal_type AND e.channel = d.channel
ORDER BY 1, 2;

-- name: UpsertEventSignalNotificationSubscription :one
-- A NULL config keeps the stored options (a new row starts from the platform
-- default), so switching a signal never resets its options.
INSERT INTO event_signal_notification_subscriptions
    (scope_event_id, signal_type, channel, enabled, audience, config)
VALUES (sqlc.arg(scope_event_id), sqlc.arg(signal_type), sqlc.arg(channel), sqlc.arg(enabled), sqlc.arg(audience),
        COALESCE(sqlc.narg(config)::jsonb,
                 (SELECT pd.config FROM platform_signal_notification_defaults pd
                  WHERE pd.signal_type = sqlc.arg(signal_type) AND pd.channel = sqlc.arg(channel)),
                 '{}'::jsonb))
ON CONFLICT (scope_event_id, signal_type, channel)
DO UPDATE SET enabled = EXCLUDED.enabled, audience = EXCLUDED.audience,
              config = COALESCE(sqlc.narg(config)::jsonb, event_signal_notification_subscriptions.config)
RETURNING signal_type, channel, enabled, audience, config;

-- name: DeleteEventSignalNotificationSubscription :execrows
DELETE FROM event_signal_notification_subscriptions
WHERE scope_event_id = $1 AND signal_type = $2 AND channel = $3;

-- name: ListPlatformSignalNotificationDefaults :many
SELECT *
FROM platform_signal_notification_defaults
ORDER BY signal_type, channel;

-- name: UpsertPlatformSignalNotificationDefault :one
INSERT INTO platform_signal_notification_defaults (signal_type, channel, enabled, audience)
VALUES ($1, $2, $3, $4)
ON CONFLICT (signal_type, channel)
DO UPDATE SET enabled = EXCLUDED.enabled, audience = EXCLUDED.audience
RETURNING *;
