-- name: CreateInApp :exec
-- An empty category stores the default tab (personal). A request whose
-- subject was decided at or after raised_at (the decision beat the
-- asynchronous delivery) is created already resolved and read.
INSERT INTO in_app_notifications
    (id, user_id, title, body, link, icon, tone, accent_color, surface, auto_dismiss_ms, actions, dismissible, scope_event_id,
     notification_type, category, action_required, subject_ref, resolved_at, resolution, resolved_by, read_at)
SELECT sqlc.arg(id),
       sqlc.arg(user_id),
       sqlc.arg(title),
       sqlc.arg(body),
       sqlc.arg(link),
       sqlc.arg(icon),
       sqlc.arg(tone),
       sqlc.arg(accent_color),
       sqlc.arg(surface),
       sqlc.arg(auto_dismiss_ms),
       sqlc.arg(actions),
       sqlc.arg(dismissible),
       sqlc.arg(scope_event_id),
       sqlc.arg(notification_type),
       COALESCE(NULLIF(sqlc.arg(category)::text, ''), 'personal'), sqlc.arg(action_required), sqlc.narg(subject_ref),
       decided.resolved_at, decided.resolution, decided.resolved_by, decided.resolved_at
FROM (SELECT 1) AS one
LEFT JOIN inbox_subject_resolutions decided
       ON sqlc.arg(action_required)::boolean
      AND decided.subject_ref = sqlc.narg(subject_ref)::text
      AND decided.resolved_at >= sqlc.narg(raised_at)::timestamptz;

-- name: ListInAppByUser :many
-- An empty event_filter lists everything; an Event id lists that Event's items and
-- items without an Event (account/system), as on the Event site (M5). The nil
-- uuid matches no Event, so it lists only items without an Event (platform sites).
-- An empty category_filter lists every tab.
SELECT n.*, COALESCE(e.name, '')::text AS event_name, COALESCE(e.tag, '')::text AS event_tag,
       COALESCE(NULLIF(btrim(r.first_name || ' ' || r.last_name), ''), r.email, '')::text AS resolved_by_name
FROM in_app_notifications n
LEFT JOIN events e ON e.id = n.scope_event_id
LEFT JOIN users r ON r.id = n.resolved_by
WHERE n.user_id = sqlc.arg(user_id)
  AND n.surface = 'inbox'
  AND (sqlc.arg(event_filter)::text = '' OR n.scope_event_id IS NULL OR n.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid)
  AND (sqlc.arg(category_filter)::text = '' OR n.category = sqlc.arg(category_filter)::text)
  AND (n.created_at, n.id) < (sqlc.arg(before_created_at)::timestamptz, sqlc.arg(before_id)::uuid)
ORDER BY n.created_at DESC, n.id DESC
LIMIT 11;

-- name: GetLatestInboxCursor :one
SELECT n.id, n.created_at
FROM in_app_notifications n
WHERE n.user_id = sqlc.arg(user_id) AND n.surface = 'inbox'
  AND (sqlc.arg(event_filter)::text = '' OR n.scope_event_id IS NULL OR n.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid)
ORDER BY n.created_at DESC, n.id DESC
LIMIT 1;

-- name: ListNewInboxSince :many
SELECT n.*, COALESCE(e.name, '')::text AS event_name, COALESCE(e.tag, '')::text AS event_tag,
       COALESCE(NULLIF(btrim(r.first_name || ' ' || r.last_name), ''), r.email, '')::text AS resolved_by_name
FROM in_app_notifications n
LEFT JOIN events e ON e.id = n.scope_event_id
LEFT JOIN users r ON r.id = n.resolved_by
WHERE n.user_id = sqlc.arg(user_id)
  AND n.surface = 'inbox'
  AND (sqlc.arg(event_filter)::text = '' OR n.scope_event_id IS NULL OR n.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid)
  AND (n.created_at, n.id) > (sqlc.arg(since_created_at)::timestamptz, sqlc.arg(since_id)::uuid)
ORDER BY n.created_at ASC, n.id ASC
LIMIT 50;

-- name: CountUnreadInbox :one
SELECT count(*)
FROM in_app_notifications n
WHERE n.user_id = sqlc.arg(user_id) AND n.surface = 'inbox' AND n.read_at IS NULL
  AND (sqlc.arg(event_filter)::text = '' OR n.scope_event_id IS NULL OR n.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid);

-- name: CountInboxByCategory :one
-- Tab badges. An item needs attention while it is an open request (whether
-- read or not) or unread; resolved requests are read, so they drop out.
-- other_events counts attention items of OTHER Events and is meaningful only
-- for an Event-id scope (the Event site's "N more in other events" line).
SELECT
    count(*) FILTER (WHERE in_scope)::bigint                                   AS all_count,
    count(*) FILTER (WHERE in_scope AND n.category = 'requests')::bigint       AS requests_count,
    count(*) FILTER (WHERE in_scope AND n.category = 'personal')::bigint       AS personal_count,
    count(*) FILTER (WHERE in_scope AND n.category = 'activity')::bigint       AS activity_count,
    count(*) FILTER (WHERE NOT in_scope AND n.scope_event_id IS NOT NULL
                       AND sqlc.arg(event_filter)::text NOT IN ('', '00000000-0000-0000-0000-000000000000'))::bigint AS other_events_count
FROM in_app_notifications n,
     LATERAL (SELECT (sqlc.arg(event_filter)::text = '' OR n.scope_event_id IS NULL
                      OR n.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid) AS in_scope) scope
WHERE n.user_id = sqlc.arg(user_id)
  AND n.surface = 'inbox'
  AND (n.read_at IS NULL OR (n.action_required AND n.resolved_at IS NULL));

-- name: ListActiveBannersByUser :many
SELECT n.*, COALESCE(e.name, '')::text AS event_name, COALESCE(e.tag, '')::text AS event_tag,
       COALESCE(NULLIF(btrim(r.first_name || ' ' || r.last_name), ''), r.email, '')::text AS resolved_by_name
FROM in_app_notifications n
LEFT JOIN events e ON e.id = n.scope_event_id
LEFT JOIN users r ON r.id = n.resolved_by
WHERE n.user_id = sqlc.arg(user_id)
  AND n.surface = 'banner'
  AND n.dismissed_at IS NULL
  AND (sqlc.arg(event_filter)::text = '' OR n.scope_event_id IS NULL OR n.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid)
ORDER BY n.created_at DESC;

-- name: MarkInAppRead :execrows
UPDATE in_app_notifications
SET read_at = now()
WHERE id = $1
  AND user_id = $2;

-- name: MarkAllInAppReadByUser :exec
-- Open requests become read but stay open: only their decision closes them.
UPDATE in_app_notifications n
SET read_at = now()
WHERE n.user_id = sqlc.arg(user_id)
  AND n.surface = 'inbox'
  AND n.read_at IS NULL
  AND (sqlc.arg(event_filter)::text = '' OR n.scope_event_id IS NULL OR n.scope_event_id = NULLIF(sqlc.arg(event_filter)::text, '')::uuid)
  AND (sqlc.arg(category_filter)::text = '' OR n.category = sqlc.arg(category_filter)::text);

-- name: DismissInAppBanner :execrows
UPDATE in_app_notifications
SET dismissed_at = now()
WHERE id = $1
  AND user_id = $2
  AND surface = 'banner'
  AND dismissible = true
  AND dismissed_at IS NULL;

-- name: GetInboxItemForUser :one
SELECT n.id, n.notification_type, n.action_required, n.subject_ref, n.resolved_at
FROM in_app_notifications n
WHERE n.id = sqlc.arg(id) AND n.user_id = sqlc.arg(user_id) AND n.surface = 'inbox';

-- name: ResolveInboxBySubjectRef :execrows
-- Closes one request for every recipient: all open copies sharing the
-- subject are resolved and marked read, and stay in the list. The decision
-- is remembered so copies still in delivery are created resolved.
WITH decision AS (
    INSERT INTO inbox_subject_resolutions (subject_ref, resolution, resolved_by, resolved_at)
    VALUES (sqlc.arg(subject_ref)::text, sqlc.arg(resolution)::text, sqlc.narg(resolved_by)::uuid, now())
    ON CONFLICT (subject_ref) DO UPDATE
        SET resolution = EXCLUDED.resolution, resolved_by = EXCLUDED.resolved_by, resolved_at = EXCLUDED.resolved_at
)
UPDATE in_app_notifications
SET resolved_at = now(),
    resolution  = sqlc.arg(resolution)::text,
    resolved_by = sqlc.narg(resolved_by)::uuid,
    read_at     = COALESCE(read_at, now())
WHERE subject_ref = sqlc.arg(subject_ref)::text
  AND action_required
  AND resolved_at IS NULL;

-- name: ResolveInboxBySubjectPattern :one
-- System resolution of every open request whose subject matches the LIKE
-- pattern (an Event's applications, a user's applications); each resolved
-- subject is remembered like a single resolution.
WITH resolved AS (
    UPDATE in_app_notifications
    SET resolved_at = now(),
        resolution  = sqlc.arg(resolution)::text,
        read_at     = COALESCE(read_at, now())
    WHERE subject_ref LIKE sqlc.arg(subject_pattern)::text
      AND action_required
      AND resolved_at IS NULL
    RETURNING subject_ref
), decision AS (
    INSERT INTO inbox_subject_resolutions (subject_ref, resolution, resolved_by, resolved_at)
    SELECT DISTINCT subject_ref, sqlc.arg(resolution)::text, NULL::uuid, now() FROM resolved
    ON CONFLICT (subject_ref) DO UPDATE
        SET resolution = EXCLUDED.resolution, resolved_by = NULL, resolved_at = EXCLUDED.resolved_at
)
SELECT count(*)::bigint AS resolved_count FROM resolved;

-- name: ResolveInboxItem :execrows
-- Closes a single request without a subject (one recipient's copy).
UPDATE in_app_notifications
SET resolved_at = now(),
    resolution  = sqlc.arg(resolution)::text,
    resolved_by = sqlc.narg(resolved_by)::uuid,
    read_at     = COALESCE(read_at, now())
WHERE id = sqlc.arg(id)
  AND user_id = sqlc.arg(user_id)
  AND action_required
  AND resolved_at IS NULL;
