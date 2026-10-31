package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/revealDeclineRepo"
)

// Owner, 2026-10-31: the «show to all» cohort is recomputed until the tasks are shown (a team that becomes eligible,
// before or after the start, joins it); once the barrier is opened it is frozen.

func setStart(t *testing.T, f *standFixture, startsIn time.Duration) {
	t.Helper()
	f.shiftLifecycle(t, startsIn, startsIn+4*time.Hour)
}

func seedBarrier(t *testing.T, f *standFixture, setID uuid.UUID, opened bool, teams ...uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	_, err := f.db.Pool.Exec(
		ctx,
		`DELETE FROM event_lab_reveal_barriers WHERE event_exercise_id=$1`,
		setID,
	)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`INSERT INTO event_lab_reveal_barriers(event_id,event_exercise_id,revision,mode,eligible_team_ids,created_at,opened_at)
		SELECT event_id,id,revision,'all_ready',$2::uuid[],now(),CASE WHEN $3 THEN now() END FROM event_exercises WHERE id=$1`,
		setID,
		teams,
		opened,
	)
	require.NoError(t, err)
}

func TestRevealCohort_IsRecomputedBeforeTheStart(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	setStart(t, f, time.Hour)
	seedBarrier(
		t,
		f,
		set.exerciseID,
		false,
		f.blueID,
	) // red became eligible after the cohort was written

	got, err := eventLabRevealRepo.New(f.db.Queries).
		Freeze(context.Background(), f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.ElementsMatch(
		t,
		[]uuid.UUID{f.blueID, f.redID},
		got,
		"a team eligible before the start joins the cohort",
	)
}

// A team that becomes eligible after the start still joins the cohort while the tasks are not shown.
func TestRevealCohort_IsRecomputedAfterTheStartUntilTheShow(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	setStart(t, f, -time.Minute)
	seedBarrier(t, f, set.exerciseID, false, f.blueID)

	got, err := eventLabRevealRepo.New(f.db.Queries).Freeze(context.Background(), f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{f.blueID, f.redID}, got)
}

// A team declined for lack of room stays out of the cohort, and the barrier forgets it.
func TestRevealCohort_ADeclinedTeamStaysOut(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	setStart(t, f, -time.Minute)
	seedBarrier(t, f, set.exerciseID, false, f.blueID, f.redID)

	store := revealDeclineRepo.New(f.db.Queries)
	require.NoError(t, store.Decline(ctx, f.eventID, set.exerciseID, f.redID, "no room", time.Now()))
	barrier, err := eventLabRevealRepo.New(f.db.Queries).Barrier(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{f.blueID}, barrier.EligibleTeamIds, "the show does not wait for the declined team")

	got, err := eventLabRevealRepo.New(f.db.Queries).Freeze(ctx, f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{f.blueID}, got, "and the recomputation does not bring it back")
}

// A decline belongs to the revision and mode it was made in: when the task changes, the team joins the new cohort again,
// and the invalidation of the barrier drops the old decline.
func TestRevealCohort_ADeclineDoesNotOutliveItsCohort(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	setStart(t, f, -time.Minute)
	seedBarrier(t, f, set.exerciseID, false, f.blueID, f.redID)
	require.NoError(t, revealDeclineRepo.New(f.db.Queries).Decline(ctx, f.eventID, set.exerciseID, f.redID, "no room", time.Now()))
	reveal := eventLabRevealRepo.New(f.db.Queries)

	// the task is edited (a new revision): the old cohort is gone, and so is the decline
	_, err := f.db.Pool.Exec(ctx, `UPDATE event_exercises SET revision=revision+1 WHERE id=$1`, set.exerciseID)
	require.NoError(t, err)
	got, err := reveal.Freeze(ctx, f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{f.blueID, f.redID}, got, "a decline of an older revision does not apply")

	require.NoError(t, reveal.Invalidate(ctx, f.eventID, set.exerciseID))
	var left int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_lab_reveal_declined WHERE event_exercise_id=$1`, set.exerciseID).Scan(&left))
	require.Zero(t, left, "the invalidation drops the declines of the old cohort")

	// a decline in the new revision applies again, and the reveal mode is part of the tuple too
	require.NoError(t, revealDeclineRepo.New(f.db.Queries).Decline(ctx, f.eventID, set.exerciseID, f.redID, "no room", time.Now()))
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_configs SET task_reveal_mode='as_ready' WHERE event_id=$1`, f.eventID)
	require.NoError(t, err)
	got, err = reveal.Freeze(ctx, f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{f.blueID, f.redID}, got, "a decline of another mode does not apply")
}

func TestRevealCohort_AnOpenedBarrierIsNeverRecomputed(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	setStart(t, f, time.Hour)
	seedBarrier(t, f, set.exerciseID, true, f.blueID)

	got, err := eventLabRevealRepo.New(f.db.Queries).
		Freeze(context.Background(), f.eventID, set.exerciseID, time.Now().UTC())
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
	_, err := f.db.Pool.Exec(
		ctx,
		`UPDATE lab_bindings SET readiness=0,deployed_at=NULL,wait_reason='cluster_reservation',wait_since=now()-interval '1 second'
		WHERE event_team_id=ANY($1)`,
		[]uuid.UUID{f.blueID, f.redID},
	)
	require.NoError(t, err)

	rows, err := f.db.Queries.ListStandTeams(ctx, postgres.ListStandTeamsParams{
		EventID:           f.eventID,
		NotDueExerciseIds: []uuid.UUID{},
		StuckBefore:       time.Now().Add(-3 * time.Minute),
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
