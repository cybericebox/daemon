package event_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
)

// Owner, 2026-10-26: «show to all» waits for every eligible team and has no timeout, but a team that is not in the
// cohort (admitted after the start) never blocks the others and never waits for the barrier: it gets each task as
// soon as its own lab is ready.
func TestPublish_TeamOutsideTheCohortDoesNotWaitForTheBarrier(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	f.uc.SetLifecycleControls(true)
	// The barrier is stuck: one eligible team's lab is not ready.
	_, err := f.db.Pool.Exec(ctx, `INSERT INTO event_lab_reveal_barriers(event_id,event_exercise_id,revision,mode,eligible_team_ids,created_at)
		SELECT event_id,id,revision,'all_ready',ARRAY[$2,$3]::uuid[],now() FROM event_exercises WHERE id=$1`, set.exerciseID, f.blueID, f.redID)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `UPDATE team_challenges SET readiness=1 WHERE event_challenge_id=ANY($1)`, set.challenges)
	require.NoError(t, err)
	published := teamChallengeRepo.NewWithRevealBarriers(f.db.Queries)

	teams, err := published.PublishAvailable(ctx, f.eventID, true)
	require.NoError(t, err)
	require.NotContains(t, teams, f.blueID, "a team of the cohort waits for the barrier")
	require.Equal(t, 0, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=ANY($2) AND readiness=2`, f.blueID, set.challenges))

	// Blue joins after the cohort was fixed: outside of it, it gets its tasks at once.
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_lab_reveal_barriers SET eligible_team_ids=array_remove(eligible_team_ids,$2) WHERE event_exercise_id=$1`, set.exerciseID, f.blueID)
	require.NoError(t, err)
	teams, err = published.PublishAvailable(ctx, f.eventID, true)
	require.NoError(t, err)
	require.Contains(t, teams, f.blueID)
	require.Equal(t, 3, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=ANY($2) AND readiness=2`, f.blueID, set.challenges))
	require.Equal(t, 0, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=ANY($2) AND readiness=2`, f.redID, set.challenges), "the cohort still waits")
}

