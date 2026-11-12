package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// khctf 2026-10-26: Labs of a deleted set stayed in event_team_labs (no FK), and from the stand deploy window on
// the stage lifecycle failed for the whole event with a bare «no rows in result set».
func TestStageLifecycle_LabOfARemovedSetDoesNotFailTheEvent(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	healthy := f.infraOne
	doomed := f.attach(t, true)
	f.pass(t)

	setOf := func(challenge uuid.UUID) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT event_exercise_id FROM event_challenges WHERE id=$1`, challenge).Scan(&id))
		return id
	}
	doomedSet, healthySet := setOf(doomed), setOf(healthy)
	require.NotZero(t, f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1`, doomedSet))

	// What the production data looked like: the set and its board rows are gone, the Lab rows stay.
	for _, q := range []string{
		`DELETE FROM lab_bindings WHERE event_challenge_id=$1`,
		`DELETE FROM team_challenges WHERE event_challenge_id=$1`,
		`DELETE FROM event_challenges WHERE id=$1`,
	} {
		_, err := f.db.Pool.Exec(ctx, q, doomed)
		require.NoError(t, err)
	}
	_, err := f.db.Pool.Exec(ctx, `DELETE FROM event_exercises WHERE id=$1`, doomedSet)
	require.NoError(t, err)

	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.ReconcileStageLabLifecycle(ctx, f.eventID, time.Now()))

	require.Zero(t, f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1 AND desired_state='Running'`, doomedSet),
		"the Labs of the removed set must be closed")
	require.NotZero(t, f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1 AND desired_state='Stopped' AND close_reason='manual'`, doomedSet))
	require.NotZero(t, f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1 AND desired_state='Running'`, healthySet),
		"the Labs of the other sets must keep running")
	// A second pass settles on the same state.
	require.NoError(t, f.uc.ReconcileStageLabLifecycle(ctx, f.eventID, time.Now()))
}

// Removing a set closes its Labs in the same transaction, so no Lab points at a missing set.
func TestDetachEventExercise_ClosesTheLabsOfTheSet(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	var set uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT event_exercise_id FROM event_challenges WHERE id=$1`, f.infraOne).Scan(&set))
	labs := f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1 AND desired_state='Running'`, set)
	require.NotZero(t, labs)

	require.NoError(t, f.uc.DetachEventExercise(ctx, f.eventID, set, false, f.ownerID))

	require.Zero(t, f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1 AND desired_state='Running'`, set))
	require.Equal(t, labs, f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1 AND close_reason='manual'`, set))
	require.Len(t, f.agent.deleted, labs, "without lifecycle controls the agent Labs are deleted at once")
	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.ReconcileStageLabLifecycle(ctx, f.eventID, time.Now()))
}
