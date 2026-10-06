-- Platform SMTP providers (scope_event_id IS NULL): a list ordered by priority.

-- name: ListPlatformSMTPProviders :many
SELECT *
FROM mail_smtp_configs
WHERE scope_event_id IS NULL
ORDER BY priority, created_at, id;

-- name: GetPlatformSMTPProvider :one
SELECT *
FROM mail_smtp_configs
WHERE id = $1
  AND scope_event_id IS NULL;

-- name: InsertPlatformSMTPProvider :one
INSERT INTO mail_smtp_configs
    (id, scope_event_id, name, priority, enabled, host, port, tls_mode, username, password_ciphertext,
     from_name, from_address, reply_to_name, reply_to_address,
     max_per_second, daily_quota, updated_by, updated_at, created_at)
VALUES ($1, NULL, $2,
        (SELECT COALESCE(MAX(p.priority) + 1, 0) FROM mail_smtp_configs p WHERE p.scope_event_id IS NULL),
        $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $16)
RETURNING *;

-- name: UpdatePlatformSMTPProvider :one
UPDATE mail_smtp_configs
SET name                = $2,
    enabled             = $3,
    host                = $4,
    port                = $5,
    tls_mode            = $6,
    username            = $7,
    password_ciphertext = $8,
    from_name           = $9,
    from_address        = $10,
    reply_to_name       = $11,
    reply_to_address    = $12,
    max_per_second      = $13,
    daily_quota         = $14,
    updated_by          = $15,
    updated_at          = $16
WHERE id = $1
  AND scope_event_id IS NULL
RETURNING *;

-- name: SetPlatformSMTPProviderEnabled :one
UPDATE mail_smtp_configs
SET enabled    = $2,
    updated_by = $3,
    updated_at = $4
WHERE id = $1
  AND scope_event_id IS NULL
RETURNING *;

-- name: DeletePlatformSMTPProvider :execrows
DELETE
FROM mail_smtp_configs
WHERE id = $1
  AND scope_event_id IS NULL;

-- Priorities become the position in the given list (0, 1, ...); providers not
-- in the list keep their order after it.
-- name: ReorderPlatformSMTPProviders :exec
UPDATE mail_smtp_configs c
SET priority = o.position - 1
FROM unnest(@ids::uuid[]) WITH ORDINALITY AS o(id, position)
WHERE c.id = o.id
  AND c.scope_event_id IS NULL;

-- Claims one message of the provider's daily allowance for the UTC day. No row =
-- the daily limit is used up. The counter restarts when the day changes.
-- name: ReservePlatformSMTPSend :one
UPDATE mail_smtp_configs
SET usage_day  = @day::date,
    sent_today = CASE WHEN usage_day = @day::date THEN sent_today + 1 ELSE 1 END
WHERE id = @id
  AND scope_event_id IS NULL
  AND (daily_quota IS NULL
    OR (CASE WHEN usage_day = @day::date THEN sent_today ELSE 0 END) < daily_quota)
RETURNING sent_today;

-- A claimed message that was not sent: gives the slot back and keeps the
-- provider error (empty = not a provider error, the last one stays).
-- name: ReleasePlatformSMTPSend :exec
UPDATE mail_smtp_configs
SET sent_today    = CASE WHEN usage_day = @day::date THEN GREATEST(sent_today - 1, 0) ELSE sent_today END,
    last_error    = CASE WHEN @error::text = '' THEN last_error ELSE @error::text END,
    last_error_at = CASE WHEN @error::text = '' THEN last_error_at ELSE @at::timestamptz END
WHERE id = @id
  AND scope_event_id IS NULL;

-- name: MarkPlatformSMTPUsed :exec
UPDATE mail_smtp_configs
SET last_used_at = @at::timestamptz
WHERE id = @id
  AND scope_event_id IS NULL;

-- name: GetEventSMTPConfig :one
SELECT *
FROM mail_smtp_configs
WHERE scope_event_id = $1;

-- name: UpsertEventSMTPConfig :one
INSERT INTO mail_smtp_configs
    (id, scope_event_id, host, port, tls_mode, username, password_ciphertext, updated_by, updated_at, max_per_second, daily_quota)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
ON CONFLICT (scope_event_id) DO UPDATE
    SET host                = EXCLUDED.host,
        port                = EXCLUDED.port,
        tls_mode            = EXCLUDED.tls_mode,
        username            = EXCLUDED.username,
        password_ciphertext = EXCLUDED.password_ciphertext,
        updated_by          = EXCLUDED.updated_by,
        updated_at          = EXCLUDED.updated_at,
        max_per_second      = EXCLUDED.max_per_second,
        daily_quota         = EXCLUDED.daily_quota
RETURNING *;

-- name: DeleteEventSMTPConfig :exec
DELETE FROM mail_smtp_configs WHERE scope_event_id = $1;

-- name: GetPlatformMailIdentity :one
SELECT *
FROM mail_identities
WHERE scope_event_id IS NULL;

-- name: GetEventMailIdentity :one
SELECT *
FROM mail_identities
WHERE scope_event_id = $1;

-- name: UpsertPlatformMailIdentity :one
INSERT INTO mail_identities
    (id, scope_event_id, from_name, from_address, reply_to_name, reply_to_address, sending_domain, updated_by, updated_at)
VALUES ($1, NULL, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT ((true)) WHERE scope_event_id IS NULL DO UPDATE
    SET from_name        = EXCLUDED.from_name,
        from_address     = EXCLUDED.from_address,
        reply_to_name    = EXCLUDED.reply_to_name,
        reply_to_address = EXCLUDED.reply_to_address,
        sending_domain   = EXCLUDED.sending_domain,
        updated_by       = EXCLUDED.updated_by,
        updated_at       = EXCLUDED.updated_at
RETURNING *;

-- name: SetPlatformMailFooter :exec
-- The platform footer (a Lexical document; NULL = the built-in default) lives
-- on the platform identity row; saving it leaves the sender fields as they are
-- (creating the row when none exists) and retires the older text footer.
INSERT INTO mail_identities (id, scope_event_id, footer_content, updated_by, updated_at)
VALUES ($1, NULL, $2, $3, $4)
ON CONFLICT ((true)) WHERE scope_event_id IS NULL DO UPDATE
    SET footer_content = EXCLUDED.footer_content,
        footer_text    = '',
        updated_by     = EXCLUDED.updated_by,
        updated_at     = EXCLUDED.updated_at;

-- name: UpsertEventMailIdentity :one
INSERT INTO mail_identities
    (id, scope_event_id, from_name, from_address, reply_to_name, reply_to_address, updated_by, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (scope_event_id) DO UPDATE
    SET from_name        = EXCLUDED.from_name,
        from_address     = EXCLUDED.from_address,
        reply_to_name    = EXCLUDED.reply_to_name,
        reply_to_address = EXCLUDED.reply_to_address,
        updated_by       = EXCLUDED.updated_by,
        updated_at       = EXCLUDED.updated_at
RETURNING *;

-- name: ListEventsDueStartReminder :many
-- Published, not withdrawn events whose reminder window has opened for the
-- current start time, whose reminder was not yet sent for that start and that
-- still deliver the reminder on some channel. The timing is the
-- days_before_start option of the email subscription (Event override, else
-- platform default, else 7).
SELECT e.id, e.tag, e.name, e.start_at, t.days_before_start
FROM events e
LEFT JOIN event_mail_settings m ON m.event_id = e.id
CROSS JOIN LATERAL (
    SELECT COALESCE((es.config ->> 'days_before_start')::int, (pd.config ->> 'days_before_start')::int, 7)::int AS days_before_start
    FROM (SELECT 1) one
    LEFT JOIN event_signal_notification_subscriptions es
      ON es.scope_event_id = e.id AND es.signal_type = 'participant.event.start_reminder' AND es.channel = 'email'
    LEFT JOIN platform_signal_notification_defaults pd
      ON pd.signal_type = 'participant.event.start_reminder' AND pd.channel = 'email'
) t
WHERE e.lifecycle_configured
  AND e.publish_at <= sqlc.arg(now_at)
  AND (e.withdraw_at IS NULL OR e.withdraw_at > sqlc.arg(now_at))
  AND e.start_at > sqlc.arg(now_at)
  AND e.start_at - make_interval(days => t.days_before_start) <= sqlc.arg(now_at)
  AND m.start_reminder_sent_for IS DISTINCT FROM e.start_at
  AND EXISTS (
      SELECT 1
      FROM platform_signal_notification_defaults d
      LEFT JOIN event_signal_notification_subscriptions o
        ON o.scope_event_id = e.id AND o.signal_type = d.signal_type AND o.channel = d.channel
      WHERE d.signal_type = 'participant.event.start_reminder' AND COALESCE(o.enabled, d.enabled))
ORDER BY e.start_at;

-- name: MarkEventStartReminderSent :exec
INSERT INTO event_mail_settings (event_id, start_reminder_sent_for, updated_at)
VALUES ($1, $2, $3)
ON CONFLICT (event_id) DO UPDATE
    SET start_reminder_sent_for = EXCLUDED.start_reminder_sent_for;

-- name: ListEventsDueFinishedNotice :many
-- Events whose effective finish passed within the last day and was not yet
-- announced (older finishes are never backfilled).
SELECT e.id, e.tag, e.name, LEAST(e.manual_finished_at, e.finish_at)::timestamptz AS finished_at
FROM events e
LEFT JOIN event_mail_settings m ON m.event_id = e.id
WHERE e.lifecycle_configured
  AND LEAST(e.manual_finished_at, e.finish_at) <= sqlc.arg(now_at)
  AND LEAST(e.manual_finished_at, e.finish_at) > sqlc.arg(now_at)::timestamptz - interval '24 hours'
  AND m.finished_notified_for IS DISTINCT FROM LEAST(e.manual_finished_at, e.finish_at)
ORDER BY 4;

-- name: MarkEventFinishedNotified :exec
INSERT INTO event_mail_settings (event_id, finished_notified_for, updated_at)
VALUES ($1, $2, $3)
ON CONFLICT (event_id) DO UPDATE
    SET finished_notified_for = EXCLUDED.finished_notified_for;

-- name: ListInvitationExpiryCandidates :many
-- Pending, not yet announced invitations of started events; the use case
-- applies the exact expiry rule of the event lifecycle.
SELECT p.event_id, p.user_id, p.invited_to_team
FROM event_participants p
JOIN events e ON e.id = p.event_id
WHERE p.invited
  AND p.status = 1
  AND p.invitation_expired_notified_at IS NULL
  AND e.lifecycle_configured
  AND e.start_at <= sqlc.arg(now_at)
ORDER BY p.event_id
LIMIT 500;

-- name: MarkInvitationExpiredNotified :execrows
UPDATE event_participants
SET invitation_expired_notified_at = $3
WHERE event_id = $1
  AND user_id = $2
  AND invitation_expired_notified_at IS NULL;
