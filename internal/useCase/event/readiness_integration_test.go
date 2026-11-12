package event_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventReadiness_ListsUnpublishedTasksAndStandsThatAreNotReady(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	_, err := f.db.Pool.Exec(
		ctx,
		`UPDATE event_challenges SET published = false WHERE id = $1`,
		f.infraOne,
	)
	require.NoError(t, err)

	view, err := f.uc.GetEventReadiness(ctx, f.eventID)
	require.NoError(t, err)
	require.False(t, view.Ready)
	require.False(t, view.Started)
	require.NotNil(t, view.StartAt)
	require.True(t, view.StandsApplicable)
	require.Equal(t, 3, view.StandsTotal, "blue, red and the moderators team")
	require.Zero(t, view.StandsReady)
	require.Len(t, view.Stands, 3, "no stand is ready yet")
	for _, stand := range view.Stands {
		require.NotEqual(t, "ready", stand.Status.String())
	}
	require.Len(t, view.UnpublishedTasks, 1)
	require.Equal(t, f.infraOne, view.UnpublishedTasks[0].ChallengeID)
	require.NotEmpty(t, view.UnpublishedTasks[0].Name)

	// everything published and every stand ready: nothing to warn about
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE event_challenges ec SET published = true FROM event_exercises ee WHERE ee.id = ec.event_exercise_id AND ee.event_id = $1`,
		f.eventID,
	)
	require.NoError(t, err)
	f.agent.setAll(f.agent.ready)
	f.pass(t)

	view, err = f.uc.GetEventReadiness(ctx, f.eventID)
	require.NoError(t, err)
	require.Empty(t, view.UnpublishedTasks)
	require.Empty(t, view.Stands)
	require.Equal(t, 3, view.StandsReady)
	require.True(t, view.Ready)
}

func TestEventReadiness_AnEventWithoutStandsOnlyChecksTheTasks(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	_, err := f.db.Pool.Exec(
		ctx,
		`UPDATE events SET infrastructure_allowed = false WHERE id = $1`,
		f.eventID,
	)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE event_challenges ec SET published = true FROM event_exercises ee WHERE ee.id = ec.event_exercise_id AND ee.event_id = $1`,
		f.eventID,
	)
	require.NoError(t, err)

	view, err := f.uc.GetEventReadiness(ctx, f.eventID)

	require.NoError(t, err)
	require.False(t, view.StandsApplicable)
	require.Empty(t, view.Stands)
	require.True(t, view.Ready)
}
