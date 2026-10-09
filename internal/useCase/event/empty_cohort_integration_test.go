package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestEmptyCohortDefersNormalModeratorsPreparationUntilFirstFormedTeam(t *testing.T) {
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	ctx := context.Background()
	// Initial synthetic roster has no formed regular team; moderators still get
	// their normal assignments. No live runtime/roster is changed by this test.
	_, err := f.db.Pool.Exec(ctx, `UPDATE events SET join_policy=1 WHERE id=$1`, f.eventID)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_teams SET formed_at=NULL WHERE event_id=$1`, f.eventID)
	require.NoError(t, err)
	f.uc.SetLifecycleControls(true)
	empty, err := eventLabRevealRepo.New(f.db.Queries).Freeze(ctx, f.eventID, set.exerciseID, time.Now().UTC())
	require.NoError(t, err, "zero cohort query must return typed defer")
	require.Empty(t, empty)
	f.pass(t)
	var frozen int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_lab_reveal_barriers WHERE event_exercise_id=$1`, set.exerciseID).Scan(&frozen))
	require.Zero(t, frozen, "moderators-only preparation must not freeze an irreversible empty regular cohort")
	require.NoError(t, f.uc.FormManagedTeam(ctx, f.eventID, f.blueID, f.ownerID))
	now := time.Now().UTC()
	r, err := calModel.NewEventReservation(calModel.EventInput{EventID: f.eventID, Window: calModel.Window{Start: now.Add(-time.Hour), End: now.Add(6 * time.Hour)}, Teams: 20, PerTeam: calModel.Amount{CPUMillicores: 10000, MemoryBytes: 10 << 30}}, f.ownerID, now)
	require.NoError(t, err)
	r.SetPlacement([]calModel.Share{{AgentID: uuid.Must(uuid.NewV7()), Units: 20}}, 0, now)
	require.NoError(t, resourceCalendarRepo.New(f.db.Queries).CreateReservation(ctx, r))
	f.uc.SetAllocationAccounting(true)
	f.pass(t)
	barrier, err := eventLabRevealRepo.New(f.db.Queries).Barrier(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{f.blueID}, barrier.EligibleTeamIds)
}

func TestExistingEmptyCohortPeriodicRepairPreservesAlreadyPreparedPins(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	var before []uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT array_agg(id ORDER BY id) FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`, f.blueID, set.challenges).Scan(&before))
	require.Len(t, before, 3)
	_, err := f.db.Pool.Exec(ctx, `INSERT INTO event_lab_reveal_barriers(event_id,event_exercise_id,revision,mode,eligible_team_ids,created_at) SELECT event_id,id,revision,'all_ready','{}'::uuid[],$2 FROM event_exercises WHERE id=$1`, set.exerciseID, time.Now().UTC())
	require.NoError(t, err)
	f.uc.SetLifecycleControls(true)
	f.pass(t)
	barrier, err := eventLabRevealRepo.New(f.db.Queries).Barrier(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.Len(t, barrier.EligibleTeamIds, 2)
	require.True(t, barrier.OpenedAt.Valid, "existing ready assignments must progress through the normal periodic path")
	var after []uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT array_agg(id ORDER BY id) FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`, f.blueID, set.challenges).Scan(&after))
	require.Equal(t, before, after)
}
