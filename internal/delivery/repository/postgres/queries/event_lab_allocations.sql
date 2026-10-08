-- Narrow ledger reads: canonical Lab rows are already shared once per generation.
-- name: ListEventLabAllocations :many
SELECT * FROM event_team_labs WHERE event_id=sqlc.arg(event_id) ORDER BY id;

-- name: ListPlatformLabAllocations :many
SELECT * FROM event_team_labs ORDER BY id;

-- name: ListEventGroupAllocations :many
SELECT * FROM event_team_group_allocations WHERE event_id=sqlc.arg(event_id) ORDER BY event_team_id;

-- name: ListPlatformGroupAllocations :many
SELECT * FROM event_team_group_allocations ORDER BY event_team_id;

-- name: CreateEventGroupAllocation :exec
INSERT INTO event_team_group_allocations(event_team_id,event_id,lab_group_name,vpn_cpu_millicores,vpn_memory_bytes,gateway_cpu_millicores,gateway_memory_bytes,plan,created_at)
VALUES(sqlc.arg(event_team_id),sqlc.arg(event_id),sqlc.arg(lab_group_name),sqlc.arg(vpn_cpu_millicores),sqlc.arg(vpn_memory_bytes),sqlc.arg(gateway_cpu_millicores),sqlc.arg(gateway_memory_bytes),sqlc.arg(plan),sqlc.arg(created_at))
ON CONFLICT(event_team_id) DO NOTHING;

-- name: GetEventPlannedMaxUsers :one
SELECT COALESCE(max(member_count),0)::bigint AS users FROM event_teams WHERE event_id=sqlc.arg(event_id) AND NOT moderators;

-- name: ListUnaccountedEventLabStarts :many
-- A historical dispatched generation with no persisted requests is unknown,
-- never a zero-capacity certificate. Unlaunched objective pins do not hold CPU.
SELECT DISTINCT l.event_id,l.event_team_id FROM event_team_labs l
JOIN lab_bindings b ON b.lab_id=l.id
WHERE b.deployed_at IS NOT NULL
AND (COALESCE((l.allocation->'AllocatedRequests'->>'CPUMillicores')::bigint,0)<=0
 OR COALESCE((l.allocation->'AllocatedRequests'->>'MemoryBytes')::bigint,0)<=0)
AND NOT COALESCE((l.desired_state<>'Running' AND l.actual_state IN ('Stopped','Deleted') AND l.agent_uid<>'' AND l.agent_generation>0 AND l.observed_revision=l.desired_revision AND l.observed_at IS NOT NULL AND l.allocation->>'RuntimeState'='Released' AND l.allocation->>'ReleasedAt' IS NOT NULL AND l.access_fenced AND l.failure_code='' AND (l.snapshot_mode='skip' OR l.snapshot_state='Succeeded')),false);

-- name: LockEventConfigForLabSizing :one
-- Configured maxima cannot change while an admission validates immutable group sizes.
SELECT event_id FROM event_configs WHERE event_id=sqlc.arg(event_id) FOR SHARE;
