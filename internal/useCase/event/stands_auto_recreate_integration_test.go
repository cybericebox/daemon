package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/autoRecreateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

// A Lab the agent accepted that then failed is tracked for automatic recreation: while it has attempts left the stand
// counts it as being created, not as failed (so the organizers are not called); after the third attempt it is failed
// again and the one alarm follows. A Lab that became ready, or an organizer's recreate, starts the count over.
func TestStandCounters_FailedLabWithAttemptsLeftIsPendingNotFailed(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	f.agent.setAll(f.agent.failed)
	f.pass(t)
	require.Equal(t, int16(2), func() int16 { r, _, _, _ := f.bindingState(t, "Blue"); return r }(), "the accepted lab failed")
	f.assertStatuses(t, map[string]string{"Blue": "failed"})

	store := autoRecreateRepo.New(f.db.Pool)
	now := time.Now()
	candidates, err := store.Candidates(ctx, f.eventID, now)
	require.NoError(t, err)
	require.NotEmpty(t, candidates)
	var blue autoRecreateRepo.Candidate
	for _, c := range candidates {
		if c.TeamID == f.blueID {
			blue = c
		}
	}
	require.Equal(t, f.blueID, blue.TeamID)
	assert.Zero(t, blue.Attempts)

	counters := func() (pending, failed int64) {
		rows, listErr := f.db.Queries.ListStandTeams(ctx, postgres.ListStandTeamsParams{EventID: f.eventID, NotDueExerciseIds: []uuid.UUID{}, StuckBefore: time.Now().Add(-3 * time.Minute)})
		require.NoError(t, listErr)
		for _, row := range rows {
			if row.ID == f.blueID {
				return row.PendingLabs, row.FailedLabs
			}
		}
		t.Fatal("blue has no stand row")
		return 0, 0
	}
	pending, failed := counters()
	assert.Zero(t, pending)
	assert.Positive(t, failed, "not tracked yet: failed as before")

	require.NoError(t, store.Track(ctx, f.blueID, blue.ExerciseID, blue.Reason, now))
	pending, failed = counters()
	assert.Positive(t, pending, "tracked with attempts left: being recreated")
	assert.Zero(t, failed)

	for i := 0; i < autoRecreateRepo.MaxAttempts; i++ {
		require.NoError(t, store.Bump(ctx, f.blueID, blue.ExerciseID, blue.Reason, now))
	}
	pending, failed = counters()
	assert.Zero(t, pending, "no attempt left")
	assert.Positive(t, failed, "failed again: the organizers hear it")
	left, err := store.Candidates(ctx, f.eventID, now)
	require.NoError(t, err)
	for _, c := range left {
		assert.NotEqual(t, f.blueID, c.TeamID, "an exhausted lab is no candidate")
	}

	require.NoError(t, store.Reset(ctx, f.blueID, blue.ExerciseID))
	again, err := store.Candidates(ctx, f.eventID, now)
	require.NoError(t, err)
	assert.NotEmpty(t, again, "a reset lab is a candidate with a fresh count")

	// A finished event recreates nothing.
	f.shiftLifecycle(t, -2*time.Hour, -time.Hour)
	none, err := store.Candidates(ctx, f.eventID, time.Now())
	require.NoError(t, err)
	assert.Empty(t, none)
}

func TestAutoRecreateBackoff(t *testing.T) {
	assert.Equal(t, []time.Duration{time.Minute, 5 * time.Minute, 15 * time.Minute},
		[]time.Duration{eventStandModel.AutoRecreateBackoff(0), eventStandModel.AutoRecreateBackoff(1), eventStandModel.AutoRecreateBackoff(2)})
	assert.Equal(t, 15*time.Minute, eventStandModel.AutoRecreateBackoff(9))
}
