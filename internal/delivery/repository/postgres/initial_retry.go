package postgres

import (
	"context"
	"github.com/gofrs/uuid"
)

// RetryNeverCreatedLabBindings is a hand-owned aggregate CAS: sqlc cannot express
// the qualified multi-table birth retry as an aggregate model method. It changes
// only readiness/error fields; all canonical identity remains immutable.
func (q *Queries) RetryNeverCreatedLabBindings(ctx context.Context, lab, team, event uuid.UUID, revision int64, operation uuid.UUID) (int64, error) {
	r, e := q.db.Exec(ctx, `UPDATE lab_bindings b SET readiness=0,failure_reason=''
FROM event_team_labs l,event_exercises e,event_challenges c
WHERE b.lab_id=$1 AND b.event_team_id=$2 AND b.event_id=$3 AND b.readiness=2 AND b.deployed_at IS NULL
 AND l.id=b.lab_id AND l.event_team_id=b.event_team_id AND l.event_id=b.event_id
 AND l.lab_group_name=b.lab_group_name AND l.lab_name=b.lab_name AND l.generation=b.generation
 AND l.desired_state='Running' AND l.logical_closed_at IS NULL AND NULLIF(l.close_reason,'') IS NULL
 AND l.desired_revision=$4 AND l.operation_id=$5 AND l.agent_uid='' AND l.agent_generation=0
 AND l.create_evidence IS NULL AND l.materialized AND NOT l.runtime_ready
 AND c.id=b.event_challenge_id AND c.event_exercise_id=l.event_exercise_id
 AND e.id=l.event_exercise_id AND e.event_id=l.event_id AND e.status=0 AND e.superseded_at IS NULL
 AND e.exercise_version_id=l.definition_version_id
 AND EXISTS(SELECT 1 FROM event_lab_objectives o WHERE o.lab_id=l.id AND o.event_challenge_id=c.id);
`, lab, team, event, revision, operation)
	return r.RowsAffected(), e
}
