-- name: ListDueRetainedEventLabs :many
SELECT l.* FROM event_team_labs l WHERE l.desired_state='Stopped' AND l.retention_until<=sqlc.arg(now)
 AND (l.protected_until IS NULL OR l.protected_until<=sqlc.arg(now))
 AND NOT EXISTS(SELECT 1 FROM event_lab_retention_pins p WHERE p.lab_id=l.id AND p.generation=l.generation AND p.needed_until>sqlc.arg(now))
ORDER BY l.retention_until,l.id LIMIT sqlc.arg(limit_val);
-- name: ListRetiringEventLabs :many
SELECT * FROM event_team_labs WHERE desired_state='Deleted' AND retirement_state<>'Deleted' ORDER BY updated_at,id LIMIT sqlc.arg(limit_val);
-- name: ListOrphanEventLabs :many
SELECT l.* FROM event_team_labs l LEFT JOIN events e ON e.id=l.event_id LEFT JOIN event_teams t ON t.id=l.event_team_id
WHERE l.desired_state='Running' AND (e.id IS NULL OR t.id IS NULL) ORDER BY l.id LIMIT sqlc.arg(limit_val);
-- name: HasEventLabRetentionPin :one
SELECT EXISTS(SELECT 1 FROM event_lab_retention_pins WHERE lab_id=sqlc.arg(lab_id) AND generation=sqlc.arg(generation) AND needed_until>sqlc.arg(now)) AS pinned;
-- name: SelectEventStageRetainedLab :exec
INSERT INTO event_stage_lab_runtime_memberships(stage_id,lab_id,generation,selected_revision,created_at)
SELECT s.id,l.id,l.generation,l.desired_revision,sqlc.arg(now) FROM event_stages s JOIN event_team_labs l ON l.event_id=s.event_id
WHERE s.id=sqlc.arg(stage_id) AND s.event_id=sqlc.arg(event_id) AND l.id=sqlc.arg(lab_id)
 AND l.close_reason IS DISTINCT FROM 'solved' AND l.desired_state<>'Deleted'
 AND EXISTS(SELECT 1 FROM lab_bindings b WHERE b.lab_id=l.id AND b.generation=l.generation AND b.lab_group_name=l.lab_group_name AND b.lab_name=l.lab_name)
ON CONFLICT DO NOTHING;
-- name: RemoveEventStageRuntimeSelections :exec
DELETE FROM event_stage_lab_runtime_memberships WHERE stage_id=sqlc.arg(stage_id);
-- name: RecomputeEventLabRetentionPins :exec
WITH future AS (
 SELECT membership.lab_id,membership.generation,s.id AS stage_id,
  s.opens_at-sqlc.arg(preparation_seconds)::bigint*interval '1 second' AS needed_from,
  s.closes_at+COALESCE(s.lab_retention_minutes,l.retention_minutes)*interval '1 minute' AS needed_until
 FROM event_stage_lab_runtime_memberships membership
 JOIN event_stages s ON s.id=membership.stage_id AND s.event_id=sqlc.arg(event_id)
 JOIN event_team_labs l ON l.id=membership.lab_id AND l.generation=membership.generation AND l.desired_state<>'Deleted'
 WHERE s.closes_at+COALESCE(s.lab_retention_minutes,l.retention_minutes)*interval '1 minute'>sqlc.arg(now)
), removed AS (
 DELETE FROM event_lab_retention_pins p USING event_team_labs l
 WHERE p.lab_id=l.id AND l.event_id=sqlc.arg(event_id)
 AND NOT EXISTS(SELECT 1 FROM future f WHERE f.lab_id=p.lab_id AND f.stage_id=p.stage_id AND f.generation=p.generation)
)
INSERT INTO event_lab_retention_pins(lab_id,stage_id,generation,needed_from,needed_until)
SELECT lab_id,stage_id,generation,needed_from,needed_until FROM future
ON CONFLICT(lab_id,stage_id,generation) DO UPDATE SET needed_from=EXCLUDED.needed_from,needed_until=EXCLUDED.needed_until;
-- name: RecomputeEventGroupRetentionPins :exec
WITH future AS (
 SELECT l.event_team_id,p.stage_id,min(p.needed_from) AS needed_from,max(p.needed_until) AS needed_until
 FROM event_lab_retention_pins p JOIN event_team_labs l ON l.id=p.lab_id AND l.event_id=sqlc.arg(event_id)
 GROUP BY l.event_team_id,p.stage_id
),removed AS (
 DELETE FROM event_lab_group_retention_pins p USING event_team_group_allocations g
 WHERE p.event_team_id=g.event_team_id AND g.event_id=sqlc.arg(event_id)
 AND NOT EXISTS(SELECT 1 FROM future f WHERE f.event_team_id=p.event_team_id AND f.stage_id=p.stage_id)
)
INSERT INTO event_lab_group_retention_pins(event_team_id,stage_id,needed_from,needed_until)
SELECT event_team_id,stage_id,needed_from,needed_until FROM future
ON CONFLICT(event_team_id,stage_id) DO UPDATE SET needed_from=EXCLUDED.needed_from,needed_until=EXCLUDED.needed_until;
-- name: RefreshEventLabProtectedUntil :exec
UPDATE event_team_labs l SET protected_until=(SELECT max(p.needed_until) FROM event_lab_retention_pins p WHERE p.lab_id=l.id AND p.generation=l.generation)
WHERE l.event_id=sqlc.arg(event_id);
-- name: RefreshEventGroupProtectedUntil :exec
UPDATE event_team_group_allocations g SET protected_until=(SELECT max(p.needed_until) FROM event_lab_group_retention_pins p WHERE p.event_team_id=g.event_team_id)
WHERE g.event_id=sqlc.arg(event_id);
-- name: ListDueStageRuntimeSelections :many
SELECT l.id AS lab_id,m.stage_id,m.selected_revision
FROM event_stage_lab_runtime_memberships m
JOIN event_stages s ON s.id=m.stage_id AND s.event_id=sqlc.arg(event_id)
JOIN event_team_labs l ON l.id=m.lab_id AND l.generation=m.generation
JOIN event_lab_retention_pins pin ON pin.lab_id=l.id AND pin.generation=l.generation AND pin.stage_id=s.id
WHERE pin.needed_from<=sqlc.arg(now) AND s.closes_at>sqlc.arg(now)
 AND m.consumed_at IS NULL AND m.selected_revision>0
 AND ((l.desired_state='Running' AND l.desired_revision=m.selected_revision)
 OR (l.desired_state='Stopped' AND (l.desired_revision=m.selected_revision OR (l.close_reason='stage' AND l.desired_revision=m.selected_revision+1))))
 AND l.close_reason IS DISTINCT FROM 'solved'
ORDER BY l.event_team_id,l.id;

-- name: LockEventStageRuntimeSelection :one
SELECT m.* FROM event_stage_lab_runtime_memberships m
JOIN event_stages s ON s.id=m.stage_id
JOIN event_team_labs l ON l.id=m.lab_id AND l.generation=m.generation
JOIN event_lab_retention_pins p ON p.lab_id=m.lab_id AND p.stage_id=m.stage_id AND p.generation=m.generation
WHERE m.stage_id=sqlc.arg(stage_id) AND m.lab_id=sqlc.arg(lab_id) AND m.generation=sqlc.arg(generation)
 AND s.event_id=sqlc.arg(event_id) AND l.event_id=s.event_id
 AND m.consumed_at IS NULL AND m.selected_revision=sqlc.arg(selected_revision) AND m.selected_revision>0
 AND p.needed_from<=sqlc.arg(now) AND s.closes_at>sqlc.arg(now)
FOR UPDATE OF m;

-- name: ConsumeEventStageRuntimeSelection :execrows
UPDATE event_stage_lab_runtime_memberships SET consumed_revision=sqlc.arg(consumed_revision),consumed_at=sqlc.arg(now)
WHERE stage_id=sqlc.arg(stage_id) AND lab_id=sqlc.arg(lab_id) AND generation=sqlc.arg(generation)
 AND selected_revision=sqlc.arg(selected_revision) AND consumed_at IS NULL;

-- name: RemoveEventSetRuntimeSelections :exec
DELETE FROM event_stage_lab_runtime_memberships m USING event_team_labs l
WHERE m.lab_id=l.id AND l.event_id=sqlc.arg(event_id) AND l.event_exercise_id=sqlc.arg(event_exercise_id);

-- name: ArchiveEventLabGeneration :exec
INSERT INTO event_lab_generations(lab_id,generation,lab_group_name,lab_name,agent_uid,operation_id,lifecycle_revision,retention_until,protected_until,actual_state,allocation)
SELECT id,generation,lab_group_name,lab_name,agent_uid,operation_id,desired_revision,retention_until,protected_until,actual_state,allocation FROM event_team_labs WHERE id=sqlc.arg(lab_id)
ON CONFLICT(lab_id,generation) DO NOTHING;
-- name: IsOwnedRetainedLabGroup :one
SELECT EXISTS(SELECT 1 FROM event_team_group_allocations g WHERE g.lab_group_name=sqlc.arg(lab_group_name)
 AND NOT(g.desired_state='Deleted' AND g.actual_state='Deleted' AND g.retirement_state='Deleted' AND g.allocation->>'StorageState'='Deleted')) AS owned;
-- name: ListDueRetainedGroups :many
SELECT g.* FROM event_team_group_allocations g WHERE g.desired_state='Stopped' AND g.actual_state='Stopped'
 AND g.retention_until<=sqlc.arg(now) AND (g.protected_until IS NULL OR g.protected_until<=sqlc.arg(now))
 AND NOT EXISTS(SELECT 1 FROM event_lab_group_retention_pins p WHERE p.event_team_id=g.event_team_id AND p.needed_until>sqlc.arg(now))
 AND NOT EXISTS(SELECT 1 FROM event_team_labs l WHERE l.event_team_id=g.event_team_id AND (l.desired_state<>'Deleted' OR l.retirement_state<>'Deleted' OR l.allocation->>'StorageState'<>'Deleted'))
ORDER BY g.retention_until,g.event_team_id LIMIT sqlc.arg(limit_val);

-- name: HasActiveEventLabRuntimeSelection :one
SELECT EXISTS(SELECT 1 FROM event_stage_lab_runtime_memberships m JOIN event_stages s ON s.id=m.stage_id
 JOIN event_lab_retention_pins p ON p.lab_id=m.lab_id AND p.stage_id=m.stage_id AND p.generation=m.generation
 WHERE m.lab_id=sqlc.arg(lab_id) AND m.generation=sqlc.arg(generation) AND m.consumed_revision=sqlc.arg(revision) AND p.needed_from<=sqlc.arg(now) AND s.closes_at>sqlc.arg(now)) AS selected;
-- name: SetEventStageRetentionPreparationDue :exec
UPDATE event_lab_retention_pins SET needed_from=sqlc.arg(needed_from)
WHERE stage_id=sqlc.arg(stage_id);
-- name: ListEventStageRuntimeMemberships :many
SELECT m.stage_id,l.id AS lab_id,l.event_team_id,l.event_exercise_id,l.definition_version_id,l.variant_index,l.generation
FROM event_stage_lab_runtime_memberships m JOIN event_stages s ON s.id=m.stage_id AND s.event_id=sqlc.arg(event_id)
JOIN event_team_labs l ON l.id=m.lab_id AND l.generation=m.generation;

-- name: IsOwnedRetainedLabReference :one
SELECT EXISTS(SELECT 1 FROM event_team_labs l WHERE l.lab_group_name=sqlc.arg(lab_group_name) AND l.lab_name=sqlc.arg(lab_name)
 AND NOT(l.desired_state='Deleted' AND l.actual_state='Deleted' AND l.retirement_state='Deleted' AND l.allocation->>'StorageState'='Deleted')) AS owned;

-- name: FinalizeRetiredLabGroupPlacement :execrows
-- Final disposal follows a persisted exact tombstone and retired children,
-- never a names-only command result or an absent monitoring frame.
DELETE FROM lab_group_placements placement USING event_team_group_allocations g
WHERE placement.lab_group_name=g.lab_group_name AND g.event_team_id=sqlc.arg(event_team_id)
 AND g.agent_uid=sqlc.arg(agent_uid) AND g.operation_id=sqlc.arg(operation_id) AND g.desired_revision=sqlc.arg(desired_revision)
 AND g.desired_state='Deleted' AND g.actual_state='Deleted' AND g.retirement_state='Deleted'
 AND g.retirement_observed_at IS NOT NULL AND g.observed_revision=g.desired_revision
 AND g.allocation->>'RuntimeState'='Released' AND g.allocation->>'StorageState'='Deleted'
 AND NOT EXISTS(SELECT 1 FROM event_team_labs l WHERE l.event_team_id=g.event_team_id
  AND (l.desired_state<>'Deleted' OR l.actual_state<>'Deleted' OR l.retirement_state<>'Deleted'
   OR l.allocation->>'StorageState'<>'Deleted' OR COALESCE((l.allocation->>'SnapshotQuotaBytes')::bigint,0)>0));
