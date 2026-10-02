-- name: CreateEvent :one
-- Timestamps come from the domain factory, not DB defaults.
-- infrastructure_allowed is set here and by UpdateEventInfrastructure only:
-- UpdateEvent never writes it.
INSERT INTO events (id, tag, name, internal_name, available_from, archive_at, lifecycle_configured,
                    created_at, created_by, updated_at, updated_by,
                    publish_at, start_at, finish_at, withdraw_at, infrastructure_allowed)
VALUES (sqlc.arg(id), sqlc.arg(tag), sqlc.arg(name), sqlc.arg(internal_name),
        sqlc.arg(available_from)::timestamptz, sqlc.narg(archive_at)::timestamptz,
        false,
        sqlc.arg(created_at), sqlc.arg(created_by), sqlc.arg(updated_at), sqlc.arg(updated_by),
        sqlc.arg(available_from)::timestamptz, sqlc.arg(available_from)::timestamptz,
        NULL, NULL, sqlc.arg(infrastructure_allowed)) RETURNING *;

-- name: GetEventByID :one
SELECT *
FROM events
WHERE id = $1;

-- name: LockEventForTeamChange :one
-- Serializes team creation inside one event while the surrounding UoW is open.
SELECT id
FROM events
WHERE id = sqlc.arg(id)
FOR UPDATE;

-- name: UpdateEvent :execrows
-- Whole-aggregate write; excludes created_at/created_by (immutable).
UPDATE events
SET tag            = sqlc.arg(tag),
    internal_name  = sqlc.arg(internal_name),
    available_from = sqlc.arg(available_from)::timestamptz,
    archive_at     = sqlc.narg(archive_at)::timestamptz,
    updated_at     = sqlc.arg(updated_at),
    updated_by     = sqlc.arg(updated_by),
    -- Until moderators configure the lifecycle, keep internal placeholders
    -- aligned with the platform window. They are never public dates.
    publish_at     = CASE WHEN NOT lifecycle_configured
                          THEN sqlc.arg(available_from)::timestamptz ELSE publish_at END,
    start_at       = CASE WHEN NOT lifecycle_configured
                          THEN sqlc.arg(available_from)::timestamptz ELSE start_at END
WHERE id = sqlc.arg(id)
  -- Optimistic lock: 0 rows means "modified since read" (or gone).
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: ArchiveEvent :execrows
-- Keep the original available_from while closing both the legacy window and
-- canonical lifecycle. A pending event has its canonical start moved just
-- before the archive instant so the lifecycle constraint remains valid.
UPDATE events
SET archive_at        = sqlc.arg(archive_at)::timestamptz,
    lifecycle_configured = true,
    publish_at        = sqlc.arg(publish_at)::timestamptz,
    start_at          = sqlc.arg(start_at)::timestamptz,
    finish_at         = sqlc.arg(finish_at)::timestamptz,
    withdraw_at       = sqlc.arg(withdraw_at)::timestamptz,
    manual_finished_at = NULL,
    updated_at        = sqlc.arg(updated_at),
    updated_by        = sqlc.arg(updated_by)
WHERE id = sqlc.arg(id)
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: UpdateEventScoringProfile :execrows
UPDATE events
SET scoring_mode             = sqlc.arg(scoring_mode),
    dynamic_algorithm        = sqlc.arg(dynamic_algorithm),
    dynamic_min_points       = sqlc.arg(dynamic_min_points),
    dynamic_max_points       = sqlc.arg(dynamic_max_points),
    dynamic_floor_at_percent = sqlc.arg(dynamic_floor_at_percent),
    force_event_scoring      = sqlc.arg(force_event_scoring),
    static_points            = sqlc.narg(static_points),
    updated_at               = sqlc.arg(updated_at),
    updated_by               = sqlc.arg(updated_by)
WHERE id = sqlc.arg(id)
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: UpdateEventInfrastructure :execrows
-- The platform administrator's flag; the use case refuses it after publication.
UPDATE events
SET infrastructure_allowed = sqlc.arg(infrastructure_allowed),
    updated_at             = sqlc.arg(updated_at),
    updated_by             = sqlc.arg(updated_by)
WHERE id = sqlc.arg(id)
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: UpdateEventLifecycle :execrows
-- Canonical event-local runtime update. It deliberately does not rewrite the
-- legacy availability window; that compatibility contract is retired only
-- after its central-admin consumers move to this lifecycle endpoint.
UPDATE events
SET join_policy        = sqlc.arg(join_policy),
    lifecycle_configured = true,
    publish_at         = sqlc.arg(publish_at),
    start_at           = sqlc.arg(start_at),
    finish_at          = sqlc.arg(finish_at),
    withdraw_at        = sqlc.arg(withdraw_at),
    manual_finished_at = sqlc.arg(manual_finished_at),
    updated_at         = sqlc.arg(updated_at),
    updated_by         = sqlc.arg(updated_by)
WHERE id = sqlc.arg(id)
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: UpdateEventPublicName :execrows
-- Event managers may change only the participant-visible name.
UPDATE events
SET name = sqlc.arg(name),
    updated_at = sqlc.arg(updated_at),
    updated_by = sqlc.arg(updated_by)
WHERE id = sqlc.arg(id)
  AND updated_at IS NOT DISTINCT FROM sqlc.arg(expected_updated_at);

-- name: DeleteEvent :execrows
DELETE
FROM events
WHERE id = $1;

-- name: ListEventsCursor :many
SELECT *
FROM events
WHERE (sqlc.arg(search)::text = '' OR tag ILIKE '%' || sqlc.arg(search)::text || '%'
    OR internal_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (created_at, id) < (sqlc.arg(cursor_created_at)::timestamptz, sqlc.arg(cursor_id)::uuid)
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg(limit_val);

-- name: CountEvents :one
SELECT count(*)
FROM events
WHERE (sqlc.arg(search)::text = '' OR tag ILIKE '%' || sqlc.arg(search)::text || '%'
    OR internal_name ILIKE '%' || sqlc.arg(search)::text || '%');

-- name: ListEventsPage :many
SELECT *
FROM events
WHERE (sqlc.arg(search)::text = '' OR tag ILIKE '%' || sqlc.arg(search)::text || '%'
    OR internal_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(status)::text = '' OR
    CASE WHEN archive_at IS NOT NULL AND archive_at <= sqlc.arg(now)::timestamptz THEN 'archived'
         WHEN available_from > sqlc.arg(now)::timestamptz THEN 'not_available'
         WHEN NOT lifecycle_configured OR publish_at > sqlc.arg(now)::timestamptz THEN 'not_published'
         WHEN withdraw_at IS NOT NULL AND withdraw_at <= sqlc.arg(now)::timestamptz THEN 'withdrawn'
         WHEN start_at > sqlc.arg(now)::timestamptz THEN 'published'
         WHEN (manual_finished_at IS NOT NULL AND manual_finished_at <= sqlc.arg(now)::timestamptz)
           OR (finish_at IS NOT NULL AND finish_at <= sqlc.arg(now)::timestamptz) THEN 'finished'
         ELSE 'started' END = sqlc.arg(status)::text)
ORDER BY
  CASE WHEN sqlc.arg(sort_by)::text = 'tag' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(tag) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'tag' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(tag) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'name' AND sqlc.arg(sort_dir)::text = 'asc' THEN lower(internal_name) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'name' AND sqlc.arg(sort_dir)::text = 'desc' THEN lower(internal_name) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'asc' THEN
    CASE WHEN archive_at IS NOT NULL AND archive_at <= sqlc.arg(now)::timestamptz THEN 6
         WHEN available_from > sqlc.arg(now)::timestamptz THEN 0
         WHEN NOT lifecycle_configured OR publish_at > sqlc.arg(now)::timestamptz THEN 1
         WHEN withdraw_at IS NOT NULL AND withdraw_at <= sqlc.arg(now)::timestamptz THEN 5
         WHEN start_at > sqlc.arg(now)::timestamptz THEN 2
         WHEN (manual_finished_at IS NOT NULL AND manual_finished_at <= sqlc.arg(now)::timestamptz)
           OR (finish_at IS NOT NULL AND finish_at <= sqlc.arg(now)::timestamptz) THEN 4
         ELSE 3 END END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'status' AND sqlc.arg(sort_dir)::text = 'desc' THEN
    CASE WHEN archive_at IS NOT NULL AND archive_at <= sqlc.arg(now)::timestamptz THEN 6
         WHEN available_from > sqlc.arg(now)::timestamptz THEN 0
         WHEN NOT lifecycle_configured OR publish_at > sqlc.arg(now)::timestamptz THEN 1
         WHEN withdraw_at IS NOT NULL AND withdraw_at <= sqlc.arg(now)::timestamptz THEN 5
         WHEN start_at > sqlc.arg(now)::timestamptz THEN 2
         WHEN (manual_finished_at IS NOT NULL AND manual_finished_at <= sqlc.arg(now)::timestamptz)
           OR (finish_at IS NOT NULL AND finish_at <= sqlc.arg(now)::timestamptz) THEN 4
         ELSE 3 END END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'availableFrom' AND sqlc.arg(sort_dir)::text = 'asc' THEN available_from END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'availableFrom' AND sqlc.arg(sort_dir)::text = 'desc' THEN available_from END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'archiveAt' AND sqlc.arg(sort_dir)::text = 'asc' THEN archive_at END ASC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'archiveAt' AND sqlc.arg(sort_dir)::text = 'desc' THEN archive_at END DESC NULLS LAST,
  CASE WHEN sqlc.arg(sort_by)::text = 'updated' AND sqlc.arg(sort_dir)::text = 'asc' THEN updated_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'updated' AND sqlc.arg(sort_dir)::text = 'desc' THEN updated_at END DESC,
  created_at DESC, id DESC
LIMIT sqlc.arg(limit_val) OFFSET sqlc.arg(offset_val);

-- name: CountEventsPage :one
SELECT count(*)
FROM events
WHERE (sqlc.arg(search)::text = '' OR tag ILIKE '%' || sqlc.arg(search)::text || '%'
    OR internal_name ILIKE '%' || sqlc.arg(search)::text || '%')
  AND (sqlc.arg(status)::text = '' OR
    CASE WHEN archive_at IS NOT NULL AND archive_at <= sqlc.arg(now)::timestamptz THEN 'archived'
         WHEN available_from > sqlc.arg(now)::timestamptz THEN 'not_available'
         WHEN NOT lifecycle_configured OR publish_at > sqlc.arg(now)::timestamptz THEN 'not_published'
         WHEN withdraw_at IS NOT NULL AND withdraw_at <= sqlc.arg(now)::timestamptz THEN 'withdrawn'
         WHEN start_at > sqlc.arg(now)::timestamptz THEN 'published'
         WHEN (manual_finished_at IS NOT NULL AND manual_finished_at <= sqlc.arg(now)::timestamptz)
           OR (finish_at IS NOT NULL AND finish_at <= sqlc.arg(now)::timestamptz) THEN 'finished'
         ELSE 'started' END = sqlc.arg(status)::text);

-- name: CountLiveEventsWithTag :one
-- Conflict means overlapping HALF-OPEN platform windows, not merely a reused
-- tag. The exclusion constraint closes the race between concurrent writers.
SELECT count(*)
FROM events
WHERE tag = $1
  AND (archive_at IS NULL OR archive_at > available_from)
  AND (sqlc.narg(archive_at)::timestamptz IS NULL OR available_from < sqlc.narg(archive_at)::timestamptz)
  AND (archive_at IS NULL OR archive_at > sqlc.arg(available_from)::timestamptz)
  AND id != sqlc.arg(exclude_id)::uuid;

-- name: GetLiveEventByTag :one
-- Resolve the tenant event from a subdomain tag: the single non-archived event
-- sharing the tag and currently inside its platform availability window.
SELECT *
FROM events
WHERE tag = $1
  AND available_from <= sqlc.arg(now)::timestamptz
  AND (archive_at IS NULL OR archive_at > sqlc.arg(now)::timestamptz)
ORDER BY available_from DESC, created_at DESC
LIMIT 1;

-- name: InsertEventScoringPopulation :one
-- The first lifecycle transition captures the population. A retry must return
-- that original immutable snapshot instead of replacing it.
INSERT INTO event_scoring_populations (event_id, units_count, captured_at)
VALUES (sqlc.arg(event_id), sqlc.arg(units_count), sqlc.arg(captured_at))
ON CONFLICT (event_id) DO UPDATE
    SET event_id = event_scoring_populations.event_id
RETURNING *;

-- name: GetEventScoringPopulation :one
SELECT *
FROM event_scoring_populations
WHERE event_id = sqlc.arg(event_id);

-- name: ListEventsDueForScoringPopulation :many
SELECT e.id AS event_id, c.participation
FROM events e
JOIN event_configs c ON c.event_id = e.id
LEFT JOIN event_scoring_populations p ON p.event_id = e.id
WHERE p.event_id IS NULL
  AND e.lifecycle_configured
  AND e.join_policy = 0
  AND e.start_at <= sqlc.arg(now)
  AND c.participation IS NOT NULL
ORDER BY e.start_at ASC;

-- name: EventTagExists :one
-- Whether any event (archived included) carries the tag. Deleted events are gone
-- from the table, so this is "the tag belongs to an event that still exists".
SELECT EXISTS (SELECT 1 FROM events WHERE tag = $1);
