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
  AND (sqlc.narg(start_from)::timestamptz IS NULL OR start_at >= sqlc.narg(start_from)::timestamptz)
  AND (sqlc.narg(start_to)::timestamptz IS NULL OR start_at < sqlc.narg(start_to)::timestamptz)
  AND (sqlc.narg(organizer_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM event_managers m WHERE m.event_id = events.id AND m.user_id = sqlc.narg(organizer_id)::uuid))
  AND (sqlc.arg(infrastructure)::text = '' OR infrastructure_allowed = (sqlc.arg(infrastructure)::text = 'true'))
  AND (sqlc.arg(needs_reservation)::text = '' OR (infrastructure_allowed AND lifecycle_configured
       AND (archive_at IS NULL OR archive_at > sqlc.arg(now)::timestamptz)
       AND (manual_finished_at IS NULL OR manual_finished_at > sqlc.arg(now)::timestamptz)
       AND (finish_at IS NULL OR finish_at > sqlc.arg(now)::timestamptz)
       AND NOT EXISTS (SELECT 1 FROM resource_reservations r WHERE r.event_id = events.id AND r.kind = 'event' AND r.canceled_at IS NULL)))
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
  CASE WHEN sqlc.arg(sort_by)::text = 'start' AND sqlc.arg(sort_dir)::text = 'asc' THEN start_at END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'start' AND sqlc.arg(sort_dir)::text = 'desc' THEN start_at END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'participants' AND sqlc.arg(sort_dir)::text = 'asc' THEN (SELECT count(*) FROM event_participants x WHERE x.event_id = events.id AND x.status = 2) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'participants' AND sqlc.arg(sort_dir)::text = 'desc' THEN (SELECT count(*) FROM event_participants x WHERE x.event_id = events.id AND x.status = 2) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'teams' AND sqlc.arg(sort_dir)::text = 'asc' THEN (SELECT count(*) FROM event_teams x WHERE x.event_id = events.id AND NOT x.moderators AND NOT x.individual) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'teams' AND sqlc.arg(sort_dir)::text = 'desc' THEN (SELECT count(*) FROM event_teams x WHERE x.event_id = events.id AND NOT x.moderators AND NOT x.individual) END DESC,
  CASE WHEN sqlc.arg(sort_by)::text = 'stands' AND sqlc.arg(sort_dir)::text = 'asc' THEN (SELECT count(*) FROM event_team_stands x JOIN event_teams t ON t.id = x.event_team_id WHERE x.event_id = events.id AND NOT t.moderators AND x.status = 2) END ASC,
  CASE WHEN sqlc.arg(sort_by)::text = 'stands' AND sqlc.arg(sort_dir)::text = 'desc' THEN (SELECT count(*) FROM event_team_stands x JOIN event_teams t ON t.id = x.event_team_id WHERE x.event_id = events.id AND NOT t.moderators AND x.status = 2) END DESC,
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
         ELSE 'started' END = sqlc.arg(status)::text)
  AND (sqlc.narg(start_from)::timestamptz IS NULL OR start_at >= sqlc.narg(start_from)::timestamptz)
  AND (sqlc.narg(start_to)::timestamptz IS NULL OR start_at < sqlc.narg(start_to)::timestamptz)
  AND (sqlc.narg(organizer_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM event_managers m WHERE m.event_id = events.id AND m.user_id = sqlc.narg(organizer_id)::uuid))
  AND (sqlc.arg(infrastructure)::text = '' OR infrastructure_allowed = (sqlc.arg(infrastructure)::text = 'true'))
  AND (sqlc.arg(needs_reservation)::text = '' OR (infrastructure_allowed AND lifecycle_configured
       AND (archive_at IS NULL OR archive_at > sqlc.arg(now)::timestamptz)
       AND (manual_finished_at IS NULL OR manual_finished_at > sqlc.arg(now)::timestamptz)
       AND (finish_at IS NULL OR finish_at > sqlc.arg(now)::timestamptz)
       AND NOT EXISTS (SELECT 1 FROM resource_reservations r WHERE r.event_id = events.id AND r.kind = 'event' AND r.canceled_at IS NULL)));

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

-- name: ListEventScheduleChanges :many
-- The latest changes of the event's schedule (who, when, from what to what), newest first.
SELECT change.*,
       COALESCE(NULLIF(btrim(person.first_name || ' ' || person.last_name), ''), person.email, '')::text AS changed_by_name
FROM event_schedule_changes change
LEFT JOIN users person ON person.id = change.changed_by
WHERE change.event_id = sqlc.arg(event_id)
ORDER BY change.changed_at DESC, change.id DESC
LIMIT 200;

-- name: ListEventCounts :many
-- The counters of the admin events table for one page of events, in one query: approved participants, teams (not the
-- moderators team, not the single-member teams of an individual event) and the stands of those teams (ready / all).
SELECT e.id                                                                      AS event_id,
       (SELECT count(*) FROM event_participants p WHERE p.event_id = e.id AND p.status = 2)::bigint AS participants,
       (SELECT count(*) FROM event_teams t WHERE t.event_id = e.id AND NOT t.moderators AND NOT t.individual)::bigint AS teams,
       (SELECT count(*) FROM event_team_stands s JOIN event_teams t ON t.id = s.event_team_id
        WHERE s.event_id = e.id AND NOT t.moderators AND s.status = 2)::bigint                      AS stands_ready,
       (SELECT count(*) FROM event_team_stands s JOIN event_teams t ON t.id = s.event_team_id
        WHERE s.event_id = e.id AND NOT t.moderators)::bigint                                       AS stands_total
FROM events e
WHERE e.id = ANY (sqlc.arg(event_ids)::uuid[]);

-- name: CountEventsFacet :many
-- How many events have each value of a field under the other filters of the table (the caller leaves the faceted
-- field's own filter empty). field: status, infrastructure or organizer (an event counts once per manager).
SELECT v.value::text AS value, count(*)::bigint AS total
FROM events
CROSS JOIN LATERAL (
    SELECT unnest(CASE sqlc.arg(field)::text
        WHEN 'status' THEN ARRAY[CASE WHEN archive_at IS NOT NULL AND archive_at <= sqlc.arg(now)::timestamptz THEN 'archived'
         WHEN available_from > sqlc.arg(now)::timestamptz THEN 'not_available'
         WHEN NOT lifecycle_configured OR publish_at > sqlc.arg(now)::timestamptz THEN 'not_published'
         WHEN withdraw_at IS NOT NULL AND withdraw_at <= sqlc.arg(now)::timestamptz THEN 'withdrawn'
         WHEN start_at > sqlc.arg(now)::timestamptz THEN 'published'
         WHEN (manual_finished_at IS NOT NULL AND manual_finished_at <= sqlc.arg(now)::timestamptz)
           OR (finish_at IS NOT NULL AND finish_at <= sqlc.arg(now)::timestamptz) THEN 'finished'
         ELSE 'started' END]
        WHEN 'infrastructure' THEN ARRAY[events.infrastructure_allowed::text]
        WHEN 'organizer' THEN COALESCE((SELECT array_agg(m.user_id::text) FROM event_managers m WHERE m.event_id = events.id), ARRAY[]::text[])
        ELSE ARRAY[]::text[] END) AS value) v
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
  AND (sqlc.narg(start_from)::timestamptz IS NULL OR start_at >= sqlc.narg(start_from)::timestamptz)
  AND (sqlc.narg(start_to)::timestamptz IS NULL OR start_at < sqlc.narg(start_to)::timestamptz)
  AND (sqlc.narg(organizer_id)::uuid IS NULL OR EXISTS (SELECT 1 FROM event_managers m WHERE m.event_id = events.id AND m.user_id = sqlc.narg(organizer_id)::uuid))
  AND (sqlc.arg(infrastructure)::text = '' OR infrastructure_allowed = (sqlc.arg(infrastructure)::text = 'true'))
  AND (sqlc.arg(needs_reservation)::text = '' OR (infrastructure_allowed AND lifecycle_configured
       AND (archive_at IS NULL OR archive_at > sqlc.arg(now)::timestamptz)
       AND (manual_finished_at IS NULL OR manual_finished_at > sqlc.arg(now)::timestamptz)
       AND (finish_at IS NULL OR finish_at > sqlc.arg(now)::timestamptz)
       AND NOT EXISTS (SELECT 1 FROM resource_reservations r WHERE r.event_id = events.id AND r.kind = 'event' AND r.canceled_at IS NULL)))
GROUP BY v.value
ORDER BY v.value;

-- name: ListEventOrganizers :many
-- Everyone who manages at least one event, for the organizer filter.
SELECT u.id, COALESCE(NULLIF(btrim(u.first_name || ' ' || u.last_name), ''), u.email)::text AS name, u.email
FROM users u
WHERE u.deleted_at IS NULL
  AND EXISTS (SELECT 1 FROM event_managers m WHERE m.user_id = u.id)
ORDER BY lower(COALESCE(NULLIF(btrim(u.first_name || ' ' || u.last_name), ''), u.email)), u.id;
