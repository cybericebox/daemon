-- name: FreezeEventLabRevealBarrier :one
INSERT INTO event_lab_reveal_barriers(event_id,event_exercise_id,revision,mode,eligible_team_ids,created_at)
SELECT ee.event_id,ee.id,ee.revision,ec.task_reveal_mode,
 COALESCE((SELECT array_agg(team.id ORDER BY team.id) FROM event_teams team WHERE team.event_id=ee.event_id
  AND NOT team.moderators AND event_team_admitted(team.event_id,team.individual,team.admitted_manually,team.admission_locked,team.member_count) AND event_team_stand_wanted(team.event_id,team.formed_at)), '{}'::uuid[]),sqlc.arg(now)
FROM event_exercises ee JOIN event_configs ec ON ec.event_id=ee.event_id
WHERE ee.id=sqlc.arg(event_exercise_id) AND ee.event_id=sqlc.arg(event_id) AND ee.status=0
ON CONFLICT(event_exercise_id) DO UPDATE SET event_exercise_id=EXCLUDED.event_exercise_id
RETURNING *;

-- name: OpenReadyEventLabRevealBarriers :execrows
UPDATE event_lab_reveal_barriers barrier SET opened_at=sqlc.arg(now)
WHERE barrier.event_id=sqlc.arg(event_id) AND barrier.opened_at IS NULL
AND cardinality(barrier.eligible_team_ids)>0
AND NOT EXISTS (
 SELECT 1 FROM unnest(barrier.eligible_team_ids) team(id)
 WHERE NOT EXISTS(SELECT 1 FROM event_team_labs l WHERE l.event_team_id=team.id AND l.event_exercise_id=barrier.event_exercise_id
  AND l.materialized AND l.agent_uid<>'' AND l.agent_generation>0 AND l.desired_state='Running' AND l.logical_closed_at IS NULL
  AND l.actual_state='Running' AND l.runtime_ready
  AND l.observed_revision=l.desired_revision
  AND NOT EXISTS(SELECT 1 FROM lab_bindings b WHERE b.lab_id=l.id AND b.readiness<>1))
);

-- name: IsEventLabManualReachable :one
SELECT EXISTS(SELECT 1 FROM event_lab_objectives o
 JOIN event_team_labs l ON l.id=o.lab_id
 JOIN team_challenges tc ON tc.event_team_id=l.event_team_id AND tc.event_challenge_id=o.event_challenge_id
 JOIN event_challenges ec ON ec.id=o.event_challenge_id AND ec.published
 JOIN event_exercises ee ON ee.id=ec.event_exercise_id AND ee.status<>2
 LEFT JOIN event_stages stage ON stage.id=ee.stage_id
 WHERE l.id=sqlc.arg(lab_id) AND tc.readiness=2 AND tc.variant_index=l.variant_index
 AND EXISTS(SELECT 1 FROM lab_bindings b WHERE b.lab_id=l.id AND b.event_team_id=l.event_team_id AND b.event_challenge_id=o.event_challenge_id AND b.generation=l.generation AND b.lab_group_name=l.lab_group_name AND b.lab_name=l.lab_name)
 AND event_stage_phase(stage.opens_at,stage.closes_at,stage.returnable,sqlc.arg(now)::timestamptz) IN (1,2)
 AND NOT EXISTS(SELECT 1 FROM event_challenge_prerequisites prerequisite WHERE prerequisite.challenge_id=tc.event_challenge_id
  AND NOT EXISTS(SELECT 1 FROM team_challenges own JOIN team_challenge_solves solved ON solved.team_challenge_id=own.id
   WHERE own.event_team_id=l.event_team_id AND own.event_challenge_id=prerequisite.prerequisite_challenge_id))) AS reachable;

-- name: GetEventLabRevealBarrier :one
SELECT * FROM event_lab_reveal_barriers WHERE event_id=sqlc.arg(event_id) AND event_exercise_id=sqlc.arg(event_exercise_id);
-- name: ListRevealSetLabs :many
SELECT l.* FROM event_team_labs l
WHERE l.event_id=sqlc.arg(event_id) AND l.event_exercise_id=sqlc.arg(event_exercise_id)
 AND l.event_team_id=ANY(sqlc.arg(team_ids)::uuid[])
 AND EXISTS(SELECT 1 FROM lab_bindings b WHERE b.lab_id=l.id AND b.generation=l.generation)
ORDER BY l.event_team_id,l.id;
