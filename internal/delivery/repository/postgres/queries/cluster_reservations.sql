-- Cluster reservations: the parts of the backend's reservations as the agents hold them, and the agent of each
-- unit of a calendar reservation. See migration 0186. The desired state is written whole under the calendar lock
-- (SaveClusterReservation); the agent's confirmation (MarkClusterReservationSynced) and the observed status
-- (ObserveClusterReservation) are narrow writes, so neither can undo a newer desired change.

-- name: SaveClusterReservation :exec
INSERT INTO cluster_reservations (id, agent_id, source, calendar_reservation_id, test_deploy_id, owner_id, starts_at, ends_at,
                                  cpu_millicores, memory_bytes, desired_rev, synced_rev, synced_at, sync_error, forced, retire_at,
                                  created_at, updated_at)
VALUES (sqlc.arg(id), sqlc.arg(agent_id), sqlc.arg(source), sqlc.narg(calendar_reservation_id), sqlc.narg(test_deploy_id), sqlc.narg(owner_id),
        sqlc.arg(starts_at), sqlc.arg(ends_at), sqlc.arg(cpu_millicores), sqlc.arg(memory_bytes), sqlc.arg(desired_rev), sqlc.arg(synced_rev),
        sqlc.narg(synced_at), sqlc.arg(sync_error), sqlc.arg(forced), sqlc.narg(retire_at), sqlc.arg(created_at), sqlc.arg(updated_at))
ON CONFLICT (id) DO UPDATE
SET starts_at = EXCLUDED.starts_at, ends_at = EXCLUDED.ends_at, cpu_millicores = EXCLUDED.cpu_millicores,
    memory_bytes = EXCLUDED.memory_bytes, desired_rev = GREATEST(cluster_reservations.desired_rev, EXCLUDED.desired_rev),
    forced = EXCLUDED.forced, retire_at = EXCLUDED.retire_at, updated_at = EXCLUDED.updated_at;

-- name: MarkClusterReservationSynced :exec
-- The agent's answer for a revision: a confirmation never lowers synced_rev and clears the failure; a failure keeps
-- synced_rev, records why and keeps the time the failures started.
UPDATE cluster_reservations
SET synced_rev        = CASE WHEN sqlc.arg(ok)::bool THEN GREATEST(synced_rev, sqlc.arg(synced_rev)) ELSE synced_rev END,
    synced_at         = CASE WHEN sqlc.arg(ok)::bool THEN sqlc.arg(at)::timestamptz ELSE synced_at END,
    sync_error        = sqlc.arg(sync_error),
    sync_reason       = sqlc.arg(sync_reason),
    sync_failed_since = CASE WHEN sqlc.arg(ok)::bool THEN NULL ELSE COALESCE(sync_failed_since, sqlc.arg(at)::timestamptz) END,
    shrink_held       = sqlc.arg(shrink_held),
    updated_at        = sqlc.arg(at)::timestamptz
WHERE id = sqlc.arg(id);

-- name: ObserveClusterReservation :exec
UPDATE cluster_reservations
SET phase = sqlc.arg(phase), notice_at = sqlc.narg(notice_at), ends_notice_at = sqlc.narg(ends_notice_at),
    ending_reason = sqlc.arg(ending_reason), used_cpu_millicores = sqlc.arg(used_cpu_millicores),
    used_memory_bytes = sqlc.arg(used_memory_bytes), group_count = sqlc.arg(group_count), exceeded = sqlc.arg(exceeded),
    observed_at = sqlc.narg(observed_at)
WHERE id = sqlc.arg(id);

-- name: DeleteClusterReservation :exec
DELETE FROM cluster_reservations WHERE id = sqlc.arg(id);

-- name: GetClusterReservation :one
SELECT * FROM cluster_reservations WHERE id = sqlc.arg(id);

-- name: ListClusterReservationsOfAgent :many
-- Every part of one agent, retired ones included (they are deleted on the agent at the next sync).
SELECT * FROM cluster_reservations WHERE agent_id = sqlc.arg(agent_id) ORDER BY starts_at, id;

-- name: ListClusterReservationsOfCalendar :many
SELECT * FROM cluster_reservations
WHERE calendar_reservation_id = sqlc.arg(calendar_reservation_id) AND source = 'calendar' AND retire_at IS NULL
ORDER BY created_at, id;

-- name: ListClusterReservationsOfCalendars :many
-- The parts of several calendar reservations, retired ones included (for the admin view).
SELECT * FROM cluster_reservations
WHERE calendar_reservation_id = ANY(sqlc.arg(ids)::uuid[]) AND source = 'calendar'
ORDER BY created_at, id;

-- name: GetClusterReservationOfTestRun :one
SELECT * FROM cluster_reservations WHERE test_deploy_id = sqlc.arg(test_deploy_id) AND source = 'test_run' AND retire_at IS NULL;

-- name: ListClusterReservationsOfTestRuns :many
-- The live test-run parts of an author's runs on one agent (the group part covers them).
SELECT * FROM cluster_reservations
WHERE source = 'test_run' AND retire_at IS NULL AND agent_id = sqlc.arg(agent_id) AND owner_id = sqlc.arg(owner_id)
ORDER BY starts_at, id;

-- name: GetClusterReservationOfTestGroup :one
SELECT * FROM cluster_reservations
WHERE owner_id = sqlc.arg(owner_id) AND source = 'test_group' AND retire_at IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: ListClusterReservationAgents :many
-- Every agent that holds a part (for the periodic reconcile).
SELECT DISTINCT agent_id FROM cluster_reservations;

-- name: ListUnsyncedClusterReservationAgents :many
SELECT DISTINCT agent_id FROM cluster_reservations WHERE synced_rev < desired_rev OR retire_at IS NOT NULL;

-- name: ListClusterReservationUnits :many
SELECT * FROM cluster_reservation_units WHERE calendar_reservation_id = sqlc.arg(calendar_reservation_id) ORDER BY assigned_at, unit_key;

-- name: AssignClusterReservationUnit :one
-- Places a unit on an agent unless it is placed already; returns the agent that holds it afterwards.
WITH ins AS (
    INSERT INTO cluster_reservation_units (calendar_reservation_id, unit_key, agent_id, assigned_at)
    VALUES (sqlc.arg(calendar_reservation_id), sqlc.arg(unit_key), sqlc.arg(agent_id), sqlc.arg(assigned_at))
    ON CONFLICT (calendar_reservation_id, unit_key) DO NOTHING
    RETURNING agent_id
)
SELECT agent_id FROM ins
UNION ALL
SELECT u.agent_id FROM cluster_reservation_units u
WHERE u.calendar_reservation_id = sqlc.arg(calendar_reservation_id) AND u.unit_key = sqlc.arg(unit_key)
LIMIT 1;

-- name: ReleaseClusterReservationUnit :exec
DELETE FROM cluster_reservation_units WHERE calendar_reservation_id = sqlc.arg(calendar_reservation_id) AND unit_key = sqlc.arg(unit_key);
