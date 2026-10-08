-- name: GetEventTeamLab :one
SELECT * FROM event_team_labs WHERE id=sqlc.arg(id);

-- name: GetEventTeamLabForChallenge :one
-- Nonlocking identity lookup. The aggregate is locked before any question row.
SELECT lab.* FROM event_team_labs lab
JOIN lab_bindings b ON b.lab_id=lab.id AND b.event_team_id=lab.event_team_id
WHERE b.event_team_id=sqlc.arg(event_team_id) AND b.event_challenge_id=sqlc.arg(event_challenge_id)
AND b.generation=lab.generation AND b.lab_group_name=lab.lab_group_name AND b.lab_name=lab.lab_name;

-- name: GetEventTeamLabForRef :one
SELECT * FROM event_team_labs WHERE event_team_id=sqlc.arg(event_team_id) AND lab_group_name=sqlc.arg(lab_group_name) AND lab_name=sqlc.arg(lab_name) AND generation=sqlc.arg(generation);

-- name: LockEventTeamLab :one
SELECT * FROM event_team_labs WHERE id=sqlc.arg(id) FOR UPDATE;

-- name: LockEventTeamForLabAdmission :one
SELECT id FROM event_teams WHERE id=sqlc.arg(id) FOR UPDATE;

-- name: CreateEventTeamLab :exec
WITH lab AS (
 INSERT INTO event_team_labs (create_evidence,runtime_stage_id,runtime_stage_known,id,event_id,event_team_id,event_exercise_id,variant_index,generation,lab_group_name,lab_name,objective_count,definition_version_id,definition_hash,retention_minutes,created_at,agent_uid,agent_generation,desired_revision,observed_revision,operation_id,desired_state,actual_state,runtime_ready,close_reason,logical_closed_at,snapshot_mode,snapshot_state,retention_until,protected_until,actual_stopped_at,observed_at,materialized,allocation,failure_code,failure_message,access_fenced,access_fenced_at,access_fence_vpn_boot_id,next_attempt_at,updated_at)
 VALUES (sqlc.narg(create_evidence),sqlc.narg(runtime_stage_id),sqlc.arg(runtime_stage_known),sqlc.arg(id),sqlc.arg(event_id),sqlc.arg(event_team_id),sqlc.arg(event_exercise_id),sqlc.arg(variant_index),sqlc.arg(generation),sqlc.arg(lab_group_name),sqlc.arg(lab_name),sqlc.arg(objective_count),sqlc.narg(definition_version_id),sqlc.arg(definition_hash),sqlc.arg(retention_minutes),sqlc.arg(created_at),sqlc.arg(agent_uid),sqlc.arg(agent_generation),sqlc.arg(desired_revision),sqlc.arg(observed_revision),sqlc.arg(operation_id),sqlc.arg(desired_state),sqlc.arg(actual_state),sqlc.arg(runtime_ready),sqlc.narg(close_reason),sqlc.narg(logical_closed_at),sqlc.arg(snapshot_mode),sqlc.arg(snapshot_state),sqlc.narg(retention_until),sqlc.narg(protected_until),sqlc.narg(actual_stopped_at),sqlc.narg(observed_at),sqlc.arg(materialized),sqlc.arg(allocation),sqlc.arg(failure_code),sqlc.arg(failure_message),sqlc.arg(access_fenced),sqlc.narg(access_fenced_at),sqlc.arg(access_fence_vpn_boot_id),sqlc.arg(next_attempt_at),sqlc.arg(updated_at)) RETURNING id
)
INSERT INTO event_lab_objectives(lab_id,event_challenge_id)
SELECT lab.id,objective FROM lab CROSS JOIN unnest(sqlc.arg(objective_ids)::uuid[]) AS objective;

-- name: UpdateEventTeamLab :execrows
UPDATE event_team_labs SET
 create_evidence=sqlc.narg(create_evidence),runtime_stage_id=sqlc.narg(runtime_stage_id),runtime_stage_known=sqlc.arg(runtime_stage_known),
 retirement_stop_target=sqlc.narg(retirement_stop_target),retirement_state=sqlc.arg(retirement_state),retirement_observed_at=sqlc.narg(retirement_observed_at),retirement_error=sqlc.arg(retirement_error),
 agent_uid=sqlc.arg(agent_uid),
 agent_generation=sqlc.arg(agent_generation),
 desired_revision=sqlc.arg(desired_revision),
 observed_revision=sqlc.arg(observed_revision),
 operation_id=sqlc.arg(operation_id),
 desired_state=sqlc.arg(desired_state),
 actual_state=sqlc.arg(actual_state),
 runtime_ready=sqlc.arg(runtime_ready),
 close_reason=sqlc.narg(close_reason),
 logical_closed_at=sqlc.narg(logical_closed_at),
 retention_minutes=sqlc.arg(retention_minutes),snapshot_mode=sqlc.arg(snapshot_mode),
 snapshot_state=sqlc.arg(snapshot_state),
 retention_until=sqlc.narg(retention_until),
 protected_until=sqlc.narg(protected_until),
 actual_stopped_at=sqlc.narg(actual_stopped_at),
 observed_at=sqlc.narg(observed_at),
 materialized=sqlc.arg(materialized),
 allocation=sqlc.arg(allocation),
 failure_code=sqlc.arg(failure_code),
 failure_message=sqlc.arg(failure_message),
 access_fenced=sqlc.arg(access_fenced),
 access_fenced_at=sqlc.narg(access_fenced_at),
 access_fence_vpn_boot_id=sqlc.arg(access_fence_vpn_boot_id),
 next_attempt_at=sqlc.arg(next_attempt_at),
 updated_at=sqlc.arg(updated_at)
WHERE id=sqlc.arg(id) AND desired_revision=sqlc.arg(expected_revision);

-- name: IsEventTeamLabComplete :one
SELECT lab.materialized AND lab.objective_count>0
 AND (SELECT count(*) FROM event_lab_objectives o WHERE o.lab_id=lab.id)=lab.objective_count
 AND NOT EXISTS (
  SELECT 1 FROM event_lab_objectives o
  LEFT JOIN lab_bindings b ON b.lab_id=o.lab_id AND b.event_challenge_id=o.event_challenge_id AND b.event_team_id=lab.event_team_id
  LEFT JOIN team_challenges tc ON tc.event_team_id=lab.event_team_id AND tc.event_challenge_id=o.event_challenge_id
  LEFT JOIN team_challenge_solves s ON s.team_challenge_id=tc.id
  LEFT JOIN team_challenge_practice_solves p ON p.team_challenge_id=tc.id
  WHERE o.lab_id=lab.id AND (b.id IS NULL OR b.generation<>lab.generation OR b.lab_group_name<>lab.lab_group_name OR b.lab_name<>lab.lab_name OR tc.id IS NULL OR tc.variant_index<>lab.variant_index OR (s.team_challenge_id IS NULL AND p.team_challenge_id IS NULL))
 ) AS complete FROM event_team_labs lab WHERE lab.id=sqlc.arg(id);

-- name: ListDirtyEventTeamLabs :many
SELECT * FROM event_team_labs
WHERE next_attempt_at<=sqlc.arg(now)
 AND NOT COALESCE((desired_state='Stopped' AND actual_state='Stopped' AND observed_revision=desired_revision AND allocation->>'RuntimeState'='Released' AND allocation->>'ReleasedAt' IS NOT NULL AND failure_code='' AND access_fenced AND (snapshot_mode='skip' OR snapshot_state='Succeeded')),false)
 AND NOT COALESCE((desired_state='Deleted' AND actual_state='Deleted' AND observed_revision=desired_revision AND allocation->>'RuntimeState'='Released' AND allocation->>'StorageState'='Deleted' AND failure_code='' AND access_fenced AND (snapshot_mode='skip' OR snapshot_state='Succeeded')),false)
ORDER BY next_attempt_at,id LIMIT sqlc.arg(limit_val);

-- name: RecordEventTeamLabInitialIdentity :execrows
UPDATE event_team_labs SET agent_uid=sqlc.arg(agent_uid),agent_generation=sqlc.arg(agent_generation),updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND agent_uid='' AND desired_revision=1 AND desired_state='Running' AND logical_closed_at IS NULL
 AND lab_group_name=sqlc.arg(lab_group_name) AND lab_name=sqlc.arg(lab_name) AND generation=sqlc.arg(generation)
 AND sqlc.arg(agent_uid)::text<>'';

-- name: RecordEventTeamLabObservation :execrows
-- Deliberate narrow write: only physical observation fields, exactly fenced.
UPDATE event_team_labs SET agent_generation=sqlc.arg(agent_generation),observed_revision=sqlc.arg(desired_revision),actual_state=sqlc.arg(actual_state),snapshot_state=sqlc.arg(snapshot_state),
 actual_stopped_at=sqlc.narg(actual_stopped_at),observed_at=sqlc.arg(observed_at),runtime_ready=(sqlc.arg(runtime_ready) AND logical_closed_at IS NULL),
 allocation=sqlc.arg(allocation),failure_code=sqlc.arg(failure_code),failure_message=sqlc.arg(failure_message),
 access_fenced=sqlc.arg(access_fenced),access_fenced_at=sqlc.narg(access_fenced_at),access_fence_vpn_boot_id=sqlc.arg(access_fence_vpn_boot_id),updated_at=sqlc.arg(observed_at)
WHERE id=sqlc.arg(id) AND agent_uid=sqlc.arg(agent_uid) AND agent_uid<>'' AND agent_generation<=sqlc.arg(agent_generation)
 AND sqlc.arg(agent_generation)::bigint>0 AND sqlc.arg(observed_generation)::bigint=sqlc.arg(agent_generation)
 AND lab_group_name=sqlc.arg(lab_group_name) AND lab_name=sqlc.arg(lab_name) AND operation_id=sqlc.arg(operation_id)
 AND desired_revision=sqlc.arg(desired_revision) AND desired_state=sqlc.arg(desired_state)
 AND (observed_at IS NULL OR observed_at<sqlc.arg(observed_at))
 AND allocation=sqlc.arg(expected_allocation)::jsonb AND updated_at=sqlc.arg(expected_updated_at)
 AND observed_at IS NOT DISTINCT FROM sqlc.narg(expected_observed_at)::timestamptz;

-- name: ListEventLabAssignmentObjectives :many
-- Includes every pinned team question, even unpublished/hidden ones.
SELECT tc.event_challenge_id,tc.variant_index, b.id AS binding_id,b.lab_id,b.lab_group_name,b.lab_name,b.generation
FROM team_challenges tc
JOIN event_challenges ec ON ec.id=tc.event_challenge_id
LEFT JOIN lab_bindings b ON b.event_team_id=tc.event_team_id AND b.event_challenge_id=tc.event_challenge_id
WHERE tc.event_id=sqlc.arg(event_id) AND tc.event_team_id=sqlc.arg(event_team_id) AND ec.event_exercise_id=sqlc.arg(event_exercise_id)
ORDER BY tc.event_challenge_id;

-- name: AttachEventLabAssignmentBindings :execrows
UPDATE lab_bindings b SET lab_id=sqlc.arg(lab_id)
FROM event_lab_objectives o,event_team_labs lab
WHERE lab.id=sqlc.arg(lab_id) AND o.lab_id=lab.id AND b.event_team_id=lab.event_team_id AND b.event_challenge_id=o.event_challenge_id
AND b.generation=lab.generation AND b.lab_group_name=lab.lab_group_name AND b.lab_name=lab.lab_name AND (b.lab_id IS NULL OR b.lab_id=lab.id);

-- name: ListEventLabAssignmentsMissingIdentity :many
SELECT DISTINCT b.event_team_id,ec.event_exercise_id
FROM lab_bindings b JOIN event_challenges ec ON ec.id=b.event_challenge_id
WHERE b.event_id=sqlc.arg(event_id) AND b.lab_id IS NULL AND b.lab_name NOT LIKE 'c-%'
ORDER BY b.event_team_id,ec.event_exercise_id;

-- name: ListPendingStoppedEventTeamLabs :many
SELECT * FROM event_team_labs
WHERE desired_state='Stopped' AND next_attempt_at<=sqlc.arg(now)
 AND NOT COALESCE((desired_state='Stopped' AND actual_state='Stopped' AND observed_revision=desired_revision AND allocation->>'RuntimeState'='Released' AND allocation->>'ReleasedAt' IS NOT NULL AND failure_code='' AND access_fenced AND (snapshot_mode='skip' OR snapshot_state='Succeeded')),false)
 AND NOT COALESCE((desired_state='Deleted' AND actual_state='Deleted' AND observed_revision=desired_revision AND allocation->>'RuntimeState'='Released' AND allocation->>'StorageState'='Deleted' AND failure_code='' AND access_fenced AND (snapshot_mode='skip' OR snapshot_state='Succeeded')),false)
ORDER BY next_attempt_at,id LIMIT sqlc.arg(limit_val);


-- name: ScheduleEventTeamLabLifecycleRetry :execrows
-- Narrow retry write; never copies physical state over a concurrent observation.
UPDATE event_team_labs SET next_attempt_at=sqlc.arg(next_attempt_at),updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND desired_revision=sqlc.arg(desired_revision) AND operation_id=sqlc.arg(operation_id);

-- name: LockEventTeamsForLabSourceChange :many
-- Team admission locks precede all Lab/question/binding locks. Include teams
-- without assignments so preparation cannot insert a new canonical Lab midway.
SELECT id FROM event_teams WHERE event_id=sqlc.arg(event_id) ORDER BY id FOR UPDATE;

-- name: LockEventTeamLabsForSourceChange :many
SELECT * FROM event_team_labs
WHERE event_id=sqlc.arg(event_id) AND event_exercise_id=sqlc.arg(event_exercise_id)
ORDER BY id FOR UPDATE;

-- name: LockEventForLabSourceChange :one
-- Serialize roster creation/change without blocking the KEY SHARE locks of
-- concurrent answer inserts while we wait for their team admission locks.
SELECT id FROM events WHERE id=sqlc.arg(id) FOR NO KEY UPDATE;

-- name: RecordEventTeamLabInitialReadiness :execrows
-- Initial deployment has no lifecycle operation status yet. Adopt current
-- readiness only for the original Running revision and matching live identity.
UPDATE event_team_labs SET runtime_ready=sqlc.arg(runtime_ready),
 actual_state=CASE WHEN sqlc.arg(runtime_ready)::boolean THEN 'Running' ELSE 'Unknown' END,
 agent_generation=sqlc.arg(agent_generation),observed_at=sqlc.arg(now),updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND desired_revision=1 AND desired_state='Running' AND logical_closed_at IS NULL
 AND agent_uid=sqlc.arg(agent_uid) AND agent_uid<>'' AND agent_generation<=sqlc.arg(agent_generation)
 AND lab_group_name=sqlc.arg(lab_group_name) AND lab_name=sqlc.arg(lab_name);

-- name: RecordEventLabRetirement :execrows
UPDATE event_team_labs SET agent_generation=sqlc.arg(agent_generation),observed_revision=sqlc.arg(observed_revision),actual_state=sqlc.arg(actual_state),allocation=sqlc.arg(allocation),retirement_state=sqlc.arg(retirement_state),retirement_observed_at=sqlc.arg(retirement_observed_at),retirement_error=sqlc.arg(retirement_error),updated_at=sqlc.arg(updated_at)
WHERE id=sqlc.arg(id) AND desired_state='Deleted' AND desired_revision=sqlc.arg(desired_revision) AND operation_id=sqlc.arg(operation_id) AND agent_uid=sqlc.arg(agent_uid)
 AND retirement_stop_target=sqlc.arg(expected_stop_target)::jsonb AND allocation=sqlc.arg(expected_allocation)::jsonb
 AND retirement_observed_at IS NOT DISTINCT FROM sqlc.narg(expected_retirement_observed_at)::timestamptz
 AND (retirement_observed_at IS NULL OR retirement_observed_at<sqlc.arg(retirement_observed_at));

-- name: RecordEventLabBirthIdentity :execrows
-- Immutable committed original birth is identity only; desired intent and all
-- compute/storage holdings survive closure/adoption and concurrent admission.
UPDATE event_team_labs SET agent_uid=sqlc.arg(agent_uid),agent_generation=sqlc.arg(agent_generation),create_evidence=sqlc.arg(create_evidence),updated_at=sqlc.arg(now)
WHERE id=sqlc.arg(id) AND agent_uid='' AND desired_state IN ('Running','Stopped')
 AND desired_revision=sqlc.arg(desired_revision) AND generation=sqlc.arg(generation)
 AND lab_group_name=sqlc.arg(lab_group_name) AND lab_name=sqlc.arg(lab_name)
 AND create_evidence=sqlc.arg(expected_create_evidence)::jsonb;
