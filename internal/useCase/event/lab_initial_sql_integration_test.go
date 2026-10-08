package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRevealRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestLegacyManagedInitialReadyCannotGrantACLButUnmanagedBindingCan(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	require.EqualValues(t, 1, lab.Revision)
	require.EqualValues(t, 0, lab.ObservedRevision)
	require.True(t, lab.RuntimeReady)
	available := func() bool {
		rows, err := f.db.Queries.ListEventLabAccessLabs(ctx, f.blueID)
		require.NoError(t, err)
		for _, r := range rows {
			if r.LabName == lab.Ref.Lab && r.Available {
				return true
			}
		}
		return false
	}
	require.False(t, available(), "generic historical initial readiness is not a managed operation receipt")
	_, err = f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET lab_id=NULL WHERE lab_id=$1`, lab.ID)
	require.NoError(t, err)
	require.True(t, available(), "explicit unmanaged compatibility must remain")
}
func TestAllReadyBarrierRejectsLegacyInitialReadinessUntilBothExactRevisions(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	now := time.Now().UTC()
	repo := eventLabRevealRepo.New(f.db.Queries)
	cohort, err := repo.Freeze(ctx, f.eventID, set.exerciseID, now)
	require.NoError(t, err)
	require.Len(t, cohort, 2)
	require.NoError(t, repo.OpenReady(ctx, f.eventID, now))
	barrier, err := repo.Barrier(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.False(t, barrier.OpenedAt.Valid, "all generic-ready copies must not publish the set")
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET observed_revision=desired_revision WHERE event_exercise_id=$1 AND event_team_id=$2`, set.exerciseID, f.blueID)
	require.NoError(t, err)
	require.NoError(t, repo.OpenReady(ctx, f.eventID, now))
	barrier, err = repo.Barrier(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.False(t, barrier.OpenedAt.Valid, "one exact team must not open another team's set")
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET observed_revision=desired_revision WHERE event_exercise_id=$1 AND event_team_id=$2`, set.exerciseID, f.redID)
	require.NoError(t, err)
	require.NoError(t, repo.OpenReady(ctx, f.eventID, now))
	barrier, err = repo.Barrier(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.True(t, barrier.OpenedAt.Valid)
}

func TestLifecyclePassRefreshesHistoricalInitialReadyFromExactObservation(t *testing.T) {
	f, set, _, _ := prepareLifecycle(t)
	ctx := context.Background()
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	require.NoError(t, err)
	require.EqualValues(t, 0, lab.ObservedRevision)
	at := time.Now().UTC()
	f.agent.mu.Lock()
	f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{lab.Ref: {Ref: lab.Ref, UID: lab.AgentUID, Generation: lab.AgentGeneration, ObservedGeneration: lab.AgentGeneration, OperationID: lab.OperationID, Revision: lab.Revision, DesiredState: "Running", ActualState: "Running", RuntimeReady: true, ObservedAt: &at, Allocation: eventLabModel.Allocation{RuntimeState: "Allocated", ObservedAt: &at}}}
	f.agent.mu.Unlock()
	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.ReconcilePendingLabLifecycles(ctx))
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	require.NoError(t, err)
	require.Equal(t, current.Revision, current.ObservedRevision)
	require.True(t, current.RuntimeReady)
}
