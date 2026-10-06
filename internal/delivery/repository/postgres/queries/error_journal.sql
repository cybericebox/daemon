-- name: UpsertErrorGroup :one
-- Reopened is true when the group was resolved and the error came back. A brand-new group has occurrences = n.
WITH prev AS (SELECT status FROM error_groups WHERE fingerprint = sqlc.arg(fingerprint)::text)
INSERT INTO error_groups (id, fingerprint, kind, source, title, status, occurrences, first_seen_at, last_seen_at, suppressed_since)
VALUES (sqlc.arg(id), sqlc.arg(fingerprint), sqlc.arg(kind), sqlc.arg(source), sqlc.arg(title), 'open', sqlc.arg(n)::bigint, sqlc.arg(at), sqlc.arg(at), sqlc.arg(n)::bigint)
ON CONFLICT (fingerprint) DO UPDATE
    SET occurrences      = error_groups.occurrences + sqlc.arg(n)::bigint,
        suppressed_since = error_groups.suppressed_since + sqlc.arg(n)::bigint,
        last_seen_at     = GREATEST(error_groups.last_seen_at, EXCLUDED.last_seen_at),
        status           = CASE WHEN error_groups.status = 'resolved' THEN 'open' ELSE error_groups.status END,
        resolved_at      = CASE WHEN error_groups.status = 'resolved' THEN NULL ELSE error_groups.resolved_at END
RETURNING error_groups.id, error_groups.fingerprint, error_groups.kind, error_groups.source, error_groups.title,
          error_groups.status, error_groups.occurrences, error_groups.first_seen_at, error_groups.last_seen_at,
          error_groups.resolved_at, error_groups.last_notified_at, error_groups.suppressed_since,
          COALESCE((SELECT status FROM prev) = 'resolved', false)::boolean AS reopened;

-- name: InsertErrorSample :exec
INSERT INTO error_samples (id, group_id, occurred_at, message, stack, method, route, http_status, request_id,
                           user_id, role, permission, limiter, details)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14);

-- name: TrimErrorSamples :exec
DELETE
FROM error_samples
WHERE group_id = sqlc.arg(gid)::uuid
  AND id NOT IN (SELECT s.id
                 FROM error_samples s
                 WHERE s.group_id = sqlc.arg(gid)::uuid
                 ORDER BY s.occurred_at DESC, s.id DESC
                 LIMIT sqlc.arg(keep)::int);

-- name: ListErrorGroups :many
SELECT id, fingerprint, kind, source, title, status, occurrences, first_seen_at, last_seen_at, resolved_at,
       last_notified_at, suppressed_since
FROM error_groups
WHERE (cardinality(sqlc.arg(kinds)::text[]) = 0 OR kind = ANY (sqlc.arg(kinds)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR last_seen_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR last_seen_at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.arg(query)::text = '' OR title ILIKE '%' || sqlc.arg(query)::text || '%'
    OR source ILIKE '%' || sqlc.arg(query)::text || '%')
  AND (sqlc.arg(request)::text = '' OR EXISTS (SELECT 1
                                               FROM error_samples s
                                               WHERE s.group_id = error_groups.id
                                                 AND s.request_id ILIKE sqlc.arg(request)::text || '%'))
ORDER BY last_seen_at DESC, id DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountErrorGroups :one
SELECT count(*)::bigint
FROM error_groups
WHERE (cardinality(sqlc.arg(kinds)::text[]) = 0 OR kind = ANY (sqlc.arg(kinds)::text[]))
  AND (sqlc.arg(status)::text = '' OR status = sqlc.arg(status)::text)
  AND (sqlc.narg(from_at)::timestamptz IS NULL OR last_seen_at >= sqlc.narg(from_at)::timestamptz)
  AND (sqlc.narg(to_at)::timestamptz IS NULL OR last_seen_at <= sqlc.narg(to_at)::timestamptz)
  AND (sqlc.arg(query)::text = '' OR title ILIKE '%' || sqlc.arg(query)::text || '%'
    OR source ILIKE '%' || sqlc.arg(query)::text || '%')
  AND (sqlc.arg(request)::text = '' OR EXISTS (SELECT 1
                                               FROM error_samples s
                                               WHERE s.group_id = error_groups.id
                                                 AND s.request_id ILIKE sqlc.arg(request)::text || '%'));

-- name: GetErrorGroup :one
SELECT id, fingerprint, kind, source, title, status, occurrences, first_seen_at, last_seen_at, resolved_at,
       last_notified_at, suppressed_since
FROM error_groups
WHERE id = $1;

-- name: ListErrorSamples :many
SELECT id, group_id, occurred_at, message, stack, method, route, http_status, request_id, user_id, role, permission,
       limiter, details
FROM error_samples
WHERE group_id = $1
ORDER BY occurred_at DESC, id DESC
LIMIT $2;

-- name: SetErrorGroupStatus :one
UPDATE error_groups
SET status      = sqlc.arg(status)::text,
    resolved_at = CASE WHEN sqlc.arg(status)::text = 'resolved' THEN sqlc.arg(now)::timestamptz ELSE NULL END
WHERE id = sqlc.arg(id)
RETURNING id, fingerprint, kind, source, title, status, occurrences, first_seen_at, last_seen_at, resolved_at,
    last_notified_at, suppressed_since;

-- name: ResolveErrorGroupByFingerprint :exec
UPDATE error_groups
SET status = 'resolved', resolved_at = $2
WHERE fingerprint = $1
  AND status = 'open';

-- name: MarkErrorGroupNotified :one
-- Claims the message of a group: only when nobody sent one since the cutoff. Returns the number of occurrences
-- since that last message (and resets it); no row means another replica was first.
UPDATE error_groups g
SET last_notified_at = sqlc.arg(now), suppressed_since = 0
FROM (SELECT e.id, e.suppressed_since
      FROM error_groups e
      WHERE e.id = sqlc.arg(id)
        AND (e.last_notified_at IS NULL OR e.last_notified_at <= sqlc.arg(cutoff))
          FOR UPDATE) AS old
WHERE g.id = old.id
RETURNING old.suppressed_since::bigint AS suppressed;

-- name: AddErrorNotFound :exec
INSERT INTO error_not_found_daily (day, route, hits)
VALUES ($1, $2, $3)
ON CONFLICT (day, route) DO UPDATE SET hits = error_not_found_daily.hits + EXCLUDED.hits;

-- name: ListErrorNotFound :many
SELECT day, route, hits
FROM error_not_found_daily
WHERE day BETWEEN sqlc.arg(from_day)::date AND sqlc.arg(to_day)::date
ORDER BY day DESC, hits DESC, route;

-- name: PurgeErrorGroups :execrows
DELETE
FROM error_groups
WHERE last_seen_at < $1;

-- name: PurgeErrorSamples :execrows
DELETE
FROM error_samples
WHERE occurred_at < $1;

-- name: PurgeErrorNotFound :execrows
DELETE
FROM error_not_found_daily
WHERE day < sqlc.arg(before)::date;

-- name: GetErrorJournalSettings :one
SELECT notify_emails, email_to_super_admins, updated_at
FROM error_journal_settings
WHERE id;

-- name: UpdateErrorJournalEmails :exec
UPDATE error_journal_settings
SET notify_emails = $1, email_to_super_admins = $2, updated_at = $3
WHERE id;

-- name: ListErrorTelegramChats :many
SELECT chat_id, label, failing, failing_since, last_error, created_at
FROM error_journal_telegram_chats
ORDER BY created_at, chat_id;

-- name: DeleteErrorTelegramChatsNotIn :exec
DELETE
FROM error_journal_telegram_chats
WHERE NOT (chat_id = ANY (sqlc.arg(keep)::text[]));

-- name: UpsertErrorTelegramChat :exec
INSERT INTO error_journal_telegram_chats (chat_id, label, created_at)
VALUES ($1, $2, $3)
ON CONFLICT (chat_id) DO UPDATE SET label = EXCLUDED.label;

-- name: SetErrorTelegramChatFailing :exec
UPDATE error_journal_telegram_chats
SET failing       = sqlc.arg(failing)::boolean,
    failing_since = CASE
                        WHEN NOT sqlc.arg(failing)::boolean THEN NULL
                        WHEN failing THEN failing_since
                        ELSE sqlc.arg(now)::timestamptz END,
    last_error    = sqlc.arg(reason)::text
WHERE chat_id = sqlc.arg(chat_id);

-- name: ListSuperAdminEmails :many
SELECT email
FROM users
WHERE role = 'super_admin'
  AND status = 'active'
ORDER BY email;
