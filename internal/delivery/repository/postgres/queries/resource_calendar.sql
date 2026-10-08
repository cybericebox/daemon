-- Resource calendar: reservations, change requests, readiness alarms, settings and test lab holds. See
-- migration 0151. Reservations are one aggregate: every change is a whole-row UPDATE.

-- name: LockResourceCalendar :exec
-- Serializes everything that decides on the calendar (reservations, placement, test lab admission) until the
-- transaction ends: no two admissions see the same free room.
SELECT pg_advisory_xact_lock(hashtextextended('resource-calendar', 0));

-- name: CreateResourceReservation :exec
INSERT INTO resource_reservations (id, kind, event_id, owner_id, starts_at, ends_at, teams,
                                   per_team_cpu_millicores, per_team_memory_bytes, per_team_snapshot_quota_bytes,
                                   largest_device_cpu_millicores, largest_device_memory_bytes,
                                   buffer_percent, dynamic_cpu_millicores, dynamic_memory_bytes, dynamic_snapshot_quota_bytes, tail_gap_seconds,
                                   size_cpu_millicores, size_memory_bytes, size_snapshot_quota_bytes, placement, unplaced,
                                   created_by, created_at, updated_at, canceled_at)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.narg(event_id), sqlc.narg(owner_id), sqlc.arg(starts_at), sqlc.arg(ends_at), sqlc.arg(teams),
        sqlc.arg(per_team_cpu_millicores), sqlc.arg(per_team_memory_bytes), sqlc.arg(per_team_snapshot_quota_bytes),
        sqlc.arg(largest_device_cpu_millicores), sqlc.arg(largest_device_memory_bytes),
        sqlc.arg(buffer_percent), sqlc.arg(dynamic_cpu_millicores), sqlc.arg(dynamic_memory_bytes), sqlc.arg(dynamic_snapshot_quota_bytes), sqlc.arg(tail_gap_seconds),
        sqlc.arg(size_cpu_millicores), sqlc.arg(size_memory_bytes), sqlc.arg(size_snapshot_quota_bytes), sqlc.arg(placement), sqlc.arg(unplaced),
        sqlc.narg(created_by), sqlc.arg(created_at), sqlc.arg(updated_at), sqlc.narg(canceled_at));

-- name: UpdateResourceReservation :execrows
UPDATE resource_reservations
SET per_team_snapshot_quota_bytes=sqlc.arg(per_team_snapshot_quota_bytes),dynamic_snapshot_quota_bytes=sqlc.arg(dynamic_snapshot_quota_bytes),size_snapshot_quota_bytes=sqlc.arg(size_snapshot_quota_bytes), starts_at = sqlc.arg(starts_at), ends_at = sqlc.arg(ends_at), teams = sqlc.arg(teams),
    per_team_cpu_millicores = sqlc.arg(per_team_cpu_millicores), per_team_memory_bytes = sqlc.arg(per_team_memory_bytes),
    largest_device_cpu_millicores = sqlc.arg(largest_device_cpu_millicores), largest_device_memory_bytes = sqlc.arg(largest_device_memory_bytes),
    buffer_percent = sqlc.arg(buffer_percent), dynamic_cpu_millicores = sqlc.arg(dynamic_cpu_millicores),
    dynamic_memory_bytes = sqlc.arg(dynamic_memory_bytes), tail_gap_seconds = sqlc.arg(tail_gap_seconds),
    size_cpu_millicores = sqlc.arg(size_cpu_millicores), size_memory_bytes = sqlc.arg(size_memory_bytes),
    placement = sqlc.arg(placement), unplaced = sqlc.arg(unplaced),
    updated_at = sqlc.arg(updated_at), canceled_at = sqlc.narg(canceled_at)
WHERE id = sqlc.arg(id);

-- name: GetResourceReservation :one
SELECT * FROM resource_reservations WHERE id = sqlc.arg(id);

-- name: GetEventResourceReservation :one
SELECT * FROM resource_reservations WHERE event_id = sqlc.arg(event_id) AND kind = 'event' AND canceled_at IS NULL;

-- name: ListResourceReservationsInWindow :many
-- The active reservations that share a slot with [from, to).
SELECT * FROM resource_reservations
WHERE canceled_at IS NULL AND starts_at < sqlc.arg(to_at) AND ends_at > sqlc.arg(from_at)
ORDER BY starts_at, id;

-- name: ListResourceReservationsEndingAfter :many
-- Every active reservation that has not ended by the given time.
SELECT * FROM resource_reservations
WHERE canceled_at IS NULL AND ends_at > sqlc.arg(after_at)
ORDER BY starts_at, id;

-- name: ListOwnedResourceBookings :many
SELECT * FROM resource_reservations
WHERE kind = 'test_booking' AND owner_id = sqlc.arg(owner_id) AND canceled_at IS NULL AND ends_at > sqlc.arg(after_at)
ORDER BY starts_at, id;

-- name: ListResourceReservationLabels :many
-- The event names of event reservations (for the admin timeline).
SELECT r.id, e.name AS event_name, e.tag AS event_tag
FROM resource_reservations r
JOIN events e ON e.id = r.event_id
WHERE r.id = ANY(sqlc.arg(ids)::uuid[]);

-- name: CreateResourceChangeRequest :exec
INSERT INTO resource_change_requests (id, reservation_id, event_id, requested_by, requested_at,
                                      size_cpu_millicores, size_memory_bytes, dynamic_cpu_millicores, dynamic_memory_bytes, size_snapshot_quota_bytes, dynamic_snapshot_quota_bytes,
                                      window_start, window_end, reason, status)
VALUES (sqlc.arg(id), sqlc.arg(reservation_id), sqlc.arg(event_id), sqlc.narg(requested_by), sqlc.arg(requested_at),
        sqlc.narg(size_cpu_millicores), sqlc.narg(size_memory_bytes), sqlc.narg(dynamic_cpu_millicores), sqlc.narg(dynamic_memory_bytes), sqlc.narg(size_snapshot_quota_bytes), sqlc.narg(dynamic_snapshot_quota_bytes),
        sqlc.narg(window_start), sqlc.narg(window_end), sqlc.arg(reason), 0);

-- name: GetResourceChangeRequest :one
SELECT * FROM resource_change_requests WHERE id = sqlc.arg(id);

-- name: DecideResourceChangeRequest :execrows
-- Writes the decision of a pending request; 0 rows: it was decided meanwhile.
UPDATE resource_change_requests
SET status = sqlc.arg(status), decided_by = sqlc.narg(decided_by), decided_at = sqlc.arg(decided_at), decision_note = sqlc.arg(decision_note)
WHERE id = sqlc.arg(id) AND status = 0;

-- name: ListResourceChangeRequests :many
-- Newest first; status and event narrow it (a null argument matches everything).
SELECT c.*, e.name AS event_name, e.tag AS event_tag
FROM resource_change_requests c
JOIN events e ON e.id = c.event_id
WHERE (sqlc.narg(status)::smallint IS NULL OR c.status = sqlc.narg(status)::smallint)
  AND (sqlc.narg(event_id)::uuid IS NULL OR c.event_id = sqlc.narg(event_id)::uuid)
ORDER BY c.requested_at DESC, c.id;

-- name: CountPendingResourceChangeRequests :one
SELECT count(*)::bigint FROM resource_change_requests WHERE status = 0;

-- name: GetOpenResourceAlarm :one
SELECT * FROM resource_alarms
WHERE kind = sqlc.arg(kind) AND reservation_id = sqlc.arg(reservation_id)
  AND COALESCE(agent_id, '00000000-0000-0000-0000-000000000000'::uuid) = COALESCE(sqlc.narg(agent_id)::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
  AND resolved_at IS NULL;

-- name: CreateResourceAlarm :exec
INSERT INTO resource_alarms (id, kind, reservation_id, event_id, agent_id, units, stage, shortage_cpu_millicores, shortage_memory_bytes, raised_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(kind), sqlc.arg(reservation_id), sqlc.narg(event_id), sqlc.narg(agent_id), sqlc.arg(units), sqlc.arg(stage),
        sqlc.arg(shortage_cpu_millicores), sqlc.arg(shortage_memory_bytes), sqlc.arg(raised_at), sqlc.arg(updated_at));

-- name: UpdateResourceAlarm :execrows
UPDATE resource_alarms
SET units = sqlc.arg(units), stage = sqlc.arg(stage), shortage_cpu_millicores = sqlc.arg(shortage_cpu_millicores), shortage_memory_bytes = sqlc.arg(shortage_memory_bytes),
    updated_at = sqlc.arg(updated_at), resolved_at = sqlc.narg(resolved_at), acked_by = sqlc.narg(acked_by), acked_at = sqlc.narg(acked_at)
WHERE id = sqlc.arg(id);

-- name: GetResourceAlarm :one
SELECT * FROM resource_alarms WHERE id = sqlc.arg(id);

-- name: ListResourceAlarms :many
-- Open alarms first, then the resolved ones, newest first; only_open leaves the resolved out.
SELECT a.*, e.name AS event_name, e.tag AS event_tag
FROM resource_alarms a
LEFT JOIN events e ON e.id = a.event_id
WHERE (NOT sqlc.arg(only_open)::bool OR a.resolved_at IS NULL)
ORDER BY (a.resolved_at IS NULL) DESC, a.raised_at DESC, a.id
LIMIT sqlc.arg(limit_val);

-- name: ListOpenResourceAlarmsOfReservation :many
SELECT * FROM resource_alarms WHERE reservation_id = sqlc.arg(reservation_id) AND resolved_at IS NULL;

-- name: GetResourceCalendarSettings :one
SELECT * FROM resource_calendar_settings WHERE id;

-- name: SetResourceCalendarSettings :exec
UPDATE resource_calendar_settings
SET test_pool_snapshot_quota_bytes=sqlc.arg(test_pool_snapshot_quota_bytes), test_pool_cpu_millicores = sqlc.arg(test_pool_cpu_millicores), test_pool_memory_bytes = sqlc.arg(test_pool_memory_bytes), updated_at = sqlc.arg(updated_at)
WHERE id;

-- name: CreateResourceTestLabHold :exec
INSERT INTO resource_test_lab_holds (id, owner_id, via, reservation_id, cpu_millicores, memory_bytes, snapshot_quota_bytes, starts_at, expires_at)
VALUES (sqlc.arg(id), sqlc.arg(owner_id), sqlc.arg(via), sqlc.narg(reservation_id), sqlc.arg(cpu_millicores), sqlc.arg(memory_bytes), sqlc.arg(snapshot_quota_bytes), sqlc.arg(starts_at), sqlc.arg(expires_at))
ON CONFLICT (id) DO UPDATE SET expires_at = EXCLUDED.expires_at, snapshot_quota_bytes=GREATEST(resource_test_lab_holds.snapshot_quota_bytes,EXCLUDED.snapshot_quota_bytes);

-- name: DeleteResourceTestLabHold :exec
DELETE FROM resource_test_lab_holds WHERE id = sqlc.arg(id);

-- name: ListActiveResourceTestLabHolds :many
SELECT * FROM resource_test_lab_holds WHERE expires_at > sqlc.arg(now) ORDER BY starts_at, id;

-- name: DeleteExpiredResourceTestLabHolds :execrows
DELETE FROM resource_test_lab_holds WHERE expires_at <= sqlc.arg(now);
