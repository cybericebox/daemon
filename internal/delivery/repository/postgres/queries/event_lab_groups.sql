-- name: GetEventTeamLabGroup :one
SELECT * FROM event_team_group_allocations WHERE event_team_id=sqlc.arg(event_team_id);
-- name: LockEventTeamLabGroup :one
SELECT * FROM event_team_group_allocations WHERE event_team_id=sqlc.arg(event_team_id) FOR UPDATE;
-- name: UpdateEventTeamLabGroup :execrows
UPDATE event_team_group_allocations SET retirement_stop_target=sqlc.narg(retirement_stop_target),retirement_state=sqlc.arg(retirement_state),retirement_observed_at=sqlc.narg(retirement_observed_at),retirement_error=sqlc.arg(retirement_error),agent_uid=sqlc.arg(agent_uid),agent_generation=sqlc.arg(agent_generation),desired_revision=sqlc.arg(desired_revision),observed_revision=sqlc.arg(observed_revision),operation_id=sqlc.arg(operation_id),desired_state=sqlc.arg(desired_state),actual_state=sqlc.arg(actual_state),ready=sqlc.arg(ready),observed_at=sqlc.narg(observed_at),allocation=sqlc.arg(allocation),access_fenced=sqlc.arg(access_fenced),failure_code=sqlc.arg(failure_code),failure_message=sqlc.arg(failure_message),pending_starts=sqlc.arg(pending_starts),retention_until=sqlc.narg(retention_until),protected_until=sqlc.narg(protected_until),next_attempt_at=sqlc.arg(next_attempt_at),updated_at=sqlc.arg(updated_at)
WHERE event_team_id=sqlc.arg(event_team_id) AND desired_revision=sqlc.arg(expected_revision);
-- name: ListEventLabGroupLifecycleWork :many
SELECT * FROM event_team_group_allocations WHERE next_attempt_at<=sqlc.arg(now) AND NOT(desired_state='Deleted' AND retirement_state='Deleted' AND actual_state='Deleted') ORDER BY next_attempt_at,event_team_id LIMIT sqlc.arg(limit_val);
-- name: LockEventTeamLifecycleLabs :many
SELECT * FROM event_team_labs WHERE event_team_id=sqlc.arg(event_team_id) ORDER BY id FOR UPDATE;
-- name: GetEventLabPendingStarts :one
SELECT count(*)::integer FROM event_team_labs WHERE event_team_id=sqlc.arg(event_team_id)
 AND desired_state='Running' AND allocation->>'RuntimeState'='Admitted' AND NOT runtime_ready;

-- name: ScheduleEventLabGroupRetry :exec
UPDATE event_team_group_allocations SET next_attempt_at=sqlc.arg(next_attempt_at)
WHERE event_team_id=sqlc.arg(event_team_id) AND operation_id=sqlc.arg(operation_id) AND desired_revision=sqlc.arg(desired_revision);
