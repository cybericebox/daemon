package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// Owner, 2026-10-30: the «show to all» cohort is recomputed up to the start of the event (a team that becomes
// eligible before the start joins it); from the start it is frozen.

func setStart(t *testing.T, f *standFixture, startsIn time.Duration) {
	t.Helper()
	f.shiftLifecycle(t, startsIn, startsIn+4*time.Hour)
}

func seedBarrier(t *testing.T, f *standFixture, setID uuid.UUID, opened bool, teams ...uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	_, err := f.db.Pool.Exec(ctx, `DELETE FROM event_lab_reveal_barriers WHERE event_exercise_id=$1`, setID)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO event_lab_reveal_barriers(event_id,event_exercise_id,revision,mode,eligible_team_ids,created_at,opened_at)
		SELECT event_id,id,revision,'all_ready',$2::uuid[],now(),CASE WHEN $3 THEN now() END FROM event_exercises WHERE id=$1`, setID, teams, opened)
	require.NoError(t, err)
}

func TestRevealCohort_IsRecomputedBeforeTheStart(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	setStart(t, f, time.Hour)
	seedBarrier(t, f, set.exerciseID, false, f.blueID) // red became eligible after the cohort was written

	got, err := eventLabRevealRepo.New(f.db.Queries).Freeze(context.Background(), f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{f.blueID, f.redID}, got, "a team eligible before the start joins the cohort")
}

func TestRevealCohort_IsFrozenFromTheStart(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	setStart(t, f, -time.Minute)
	seedBarrier(t, f, set.exerciseID, false, f.blueID)

	got, err := eventLabRevealRepo.New(f.db.Queries).Freeze(context.Background(), f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{f.blueID}, got, "after the start a late team stays outside")
}

func TestRevealCohort_AnOpenedBarrierIsNeverRecomputed(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	setStart(t, f, time.Hour)
	seedBarrier(t, f, set.exerciseID, true, f.blueID)

	got, err := eventLabRevealRepo.New(f.db.Queries).Freeze(context.Background(), f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{f.blueID}, got, "tasks of an opened cohort are already shown")
}

// A team outside the frozen cohort is not given the three-minute grace: its wait is reported at once. A team of the
// cohort keeps the grace.
func TestRevealCohort_LateTeamWaitIsReportedAtOnce(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	setStart(t, f, -time.Minute)
	seedBarrier(t, f, set.exerciseID, false, f.blueID) // red is outside
	_, err := f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET readiness=0,deployed_at=NULL,wait_reason='cluster_reservation',wait_since=now()-interval '1 second'
		WHERE event_team_id=ANY($1)`, []uuid.UUID{f.blueID, f.redID})
	require.NoError(t, err)

	rows, err := f.db.Queries.ListStandTeams(ctx, postgres.ListStandTeamsParams{
		EventID: f.eventID, NotDueExerciseIds: []uuid.UUID{}, StuckBefore: time.Now().Add(-3 * time.Minute),
	})
	require.NoError(t, err)
	stuck := map[uuid.UUID]int64{}
	reason := map[uuid.UUID]string{}
	for _, row := range rows {
		stuck[row.ID], reason[row.ID] = row.StuckLabs, row.StuckReason
	}
	require.Zero(t, stuck[f.blueID], "the cohort waits its three minutes")
	require.Positive(t, stuck[f.redID], "the late team is reported at once")
	require.Equal(t, "cluster_reservation", reason[f.redID])
}
